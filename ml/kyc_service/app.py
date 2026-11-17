"""
kyc_service — document intelligence + liveness sidecar (Tranche 22).

Architecture (openKYC/Ballerine-inspired, assembled from license-clean OSS):

  ID document ──► PaddleOCR (PP-OCRv5 mobile, Apache-2.0) ──► raw text+boxes
       │                                                       │
       ├── Docling (MIT) for PDF layout/tables ◄───────────────┘
       ▼
  Field extraction: regex/MRZ parsers over OCR text first,
  optional VLM (ollama MiniCPM-V) as second pass — ALWAYS cross-validated
  against OCR text (VLM digits are never trusted unverified).
       │
  Selfie ──► YuNet face detect (Apache-2.0, OpenCV Zoo)
       ├──► SFace 128-d embedding → cosine match vs ID portrait
       └──► MiniFASNetV1SE+V2 dual-scale ensemble (Silent-Face-
            Anti-Spoofing, Apache-2.0, ONNX, ~20ms CPU) → liveness

Fail-closed contract: every endpoint returns {"ok": false, ...} on any
error or missing model — the Go caller falls back to manual review.

Endpoints:
  GET  /healthz
  POST /ocr            {"image_b64": ...}                     → texts/boxes
  POST /parse          {"doc_b64": ..., "filename": ...}      → docling text
  POST /extract        {"image_b64":..., "doc_type":...}      → fields JSON
  POST /face/compare   {"id_image_b64":..., "selfie_b64":...} → similarity
  POST /liveness       {"image_b64": ...}                     → real/spoof
"""

import base64
import io
import json
import os
import re
import sys
import time
import traceback
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

MODELS_DIR = os.environ.get("KYC_MODELS_DIR", os.path.join(os.path.dirname(__file__), "weights"))
MAX_BODY = 20 << 20  # 20MB

# ---------------------------------------------------------------- lazily loaded engines
_ocr = None
_face = None
_live = None
_docling = None


def _load_ocr():
    global _ocr
    if _ocr is None:
        from paddleocr import PaddleOCR
        _ocr = PaddleOCR(use_doc_orientation_classify=False,
                         use_doc_unwarping=False,
                         use_textline_orientation=False)
    return _ocr


def _load_face():
    """YuNet detector + SFace recognizer from OpenCV Zoo (Apache-2.0)."""
    global _face
    if _face is None:
        import cv2
        det_path = os.path.join(MODELS_DIR, "face_detection_yunet_2023mar.onnx")
        rec_path = os.path.join(MODELS_DIR, "face_recognition_sface_2021dec.onnx")
        if not (os.path.exists(det_path) and os.path.exists(rec_path)):
            raise FileNotFoundError("yunet/sface ONNX not in " + MODELS_DIR)
        det = cv2.FaceDetectorYN.create(det_path, "", (320, 320))
        rec = cv2.FaceRecognizerSF.create(rec_path, "")
        _face = (det, rec)
    return _face


def _load_liveness():
    """MiniFASNetV1SE + V2 dual-scale ONNX ensemble."""
    global _live
    if _live is None:
        import onnxruntime as ort
        specs = [
            ("MiniFASNetV2.onnx", 2.7),
            ("MiniFASNetV1SE.onnx", 4.0),
        ]
        nets = []
        for name, scale in specs:
            p = os.path.join(MODELS_DIR, name)
            if not os.path.exists(p):
                raise FileNotFoundError(name + " not in " + MODELS_DIR)
            nets.append((ort.InferenceSession(p, providers=["CPUExecutionProvider"]), scale))
        _live = nets
    return _live


def _load_docling():
    global _docling
    if _docling is None:
        from docling.document_converter import DocumentConverter
        _docling = DocumentConverter()
    return _docling


# ---------------------------------------------------------------- image helpers

def b64_to_bgr(image_b64):
    import cv2
    import numpy as np
    raw = base64.b64decode(image_b64)
    arr = np.frombuffer(raw, dtype=np.uint8)
    img = cv2.imdecode(arr, cv2.IMREAD_COLOR)
    if img is None:
        raise ValueError("undecodable image")
    return img


# ---------------------------------------------------------------- OCR + parsing

def do_ocr(image_b64):
    import numpy as np
    img = b64_to_bgr(image_b64)
    ocr = _load_ocr()
    res = ocr.predict(img)
    texts, boxes, scores = [], [], []
    for page in res:
        r = page.json["res"] if hasattr(page, "json") else page
        for t, b, s in zip(r.get("rec_texts", []), r.get("rec_boxes", []), r.get("rec_scores", [])):
            texts.append(t)
            boxes.append([int(x) for x in (b.tolist() if hasattr(b, "tolist") else b)])
            scores.append(float(s))
    return {"texts": texts, "boxes": boxes, "scores": scores,
            "avg_confidence": (sum(scores) / len(scores)) if scores else 0.0}


def do_parse(doc_b64, filename):
    raw = base64.b64decode(doc_b64)
    import tempfile
    with tempfile.NamedTemporaryFile(suffix=os.path.splitext(filename or "doc.pdf")[1], delete=False) as f:
        f.write(raw)
        path = f.name
    try:
        conv = _load_docling()
        result = conv.convert(path)
        doc = result.document
        md = doc.export_to_markdown()
        blocks = []
        for item, _ in doc.iterate_items():
            txt = getattr(item, "text", None) or ""
            if txt.strip():
                blocks.append({"type": type(item).__name__, "text": txt})
        return {"markdown": md, "blocks": blocks}
    finally:
        os.unlink(path)


# Nigerian ID field patterns — NIN is 11 digits; DOB formats vary.
RE_NIN = re.compile(r"\b(\d{11})\b")
RE_DATE = re.compile(r"\b(\d{2}[/\-.]\d{2}[/\-.]\d{4}|\d{4}[/\-.]\d{2}[/\-.]\d{2})\b")
RE_EXPIRY_HINT = re.compile(r"expir|valid\s*(until|thru)|exp\.?", re.I)
RE_DOB_HINT = re.compile(r"birth|dob|b\.?date", re.I)
RE_INSTITUTION = re.compile(
    r"commission|national|identity|management|republic|federal|passport|"
    r"licen[cs]e|authority|government|ministry|driver|permanent|voter", re.I)
RE_BVN = re.compile(r"\b(\d{11})\b")
RE_RC = re.compile(r"\bRC\s*[:#]?\s*(\d{5,7})\b", re.I)


def _iso_date(d):
    """Normalise DD/MM/YYYY|DD-MM-YYYY|YYYY-MM-DD to ISO YYYY-MM-DD."""
    if not d:
        return d
    for sep in ("/", "-", "."):
        if sep in d:
            parts = d.split(sep)
            if len(parts) != 3:
                return d
            if len(parts[0]) == 4:  # already YYYY-first
                return "%s-%02d-%02d" % (parts[0], int(parts[1]), int(parts[2]))
            return "%s-%02d-%02d" % (parts[2], int(parts[1]), int(parts[0]))  # DD-first (NG convention)
    return d


def extract_fields_regex(texts):
    """Deterministic first-pass extraction over OCR lines."""
    joined = "\n".join(texts)
    fields = {"full_name": None, "date_of_birth": None, "document_number": None,
              "expiry_date": None, "rc_number": None}
    for m in RE_RC.finditer(joined):
        fields["rc_number"] = "RC" + m.group(1)
    nums = RE_NIN.findall(joined)
    if nums:
        fields["document_number"] = nums[0]
        fields["nin"] = nums[0]  # alias: 11-digit doc number on Nigerian IDs
    lines = texts if isinstance(texts, list) else joined.split("\n")
    for i, ln in enumerate(lines):
        if RE_DOB_HINT.search(ln):
            d = RE_DATE.search(ln)
            if not d and i + 1 < len(lines):
                d = RE_DATE.search(lines[i + 1])
            if d:
                fields["date_of_birth"] = _iso_date(d.group(1))
        if RE_EXPIRY_HINT.search(ln):
            d = RE_DATE.search(ln)
            if not d and i + 1 < len(lines):
                d = RE_DATE.search(lines[i + 1])
            if d:
                fields["expiry_date"] = _iso_date(d.group(1))
    # name heuristic: the longest all-caps-ish line with 2-3 words
    best = ""
    for ln in lines:
        words = ln.strip().split()
        if 2 <= len(words) <= 4 and ln.upper() == ln and not RE_DATE.search(ln) \
                and not RE_EXPIRY_HINT.search(ln) and not RE_DOB_HINT.search(ln) \
                and not RE_INSTITUTION.search(ln):
            if len(ln) > len(best or ""):
                best = ln.strip()
    if best:
        fields["full_name"] = best.title()
    return fields


def do_extract(image_b64, doc_type):
    """OCR → regex fields → optional VLM pass, cross-validated."""
    ocr_out = do_ocr(image_b64)
    fields = extract_fields_regex(ocr_out["texts"])
    vlm_used = False
    vlm_url = os.environ.get("OLLAMA_URL", "")
    vlm_model = os.environ.get("KYC_VLM_MODEL", "minicpm-v4.6")
    if vlm_url:
        try:
            import json as _json
            import urllib.request
            prompt = ("Extract from this %s as strict JSON only: full_name, "
                      "date_of_birth, document_number, expiry_date, nationality." % (doc_type or "ID document"))
            body = _json.dumps({"model": vlm_model, "stream": False, "format": "json",
                                "messages": [{"role": "user", "content": prompt,
                                              "images": [image_b64]}]}).encode()
            req = urllib.request.Request(vlm_url.rstrip("/") + "/api/chat", data=body)
            with urllib.request.urlopen(req, timeout=120) as resp:
                payload = _json.loads(resp.read().decode())
            vlm_fields = _json.loads(payload["message"]["content"])
            # cross-validate: accept VLM value only if it appears in OCR text
            ocr_joined = " ".join(ocr_out["texts"]).lower()
            for k, v in vlm_fields.items():
                if not v:
                    continue
                sv = str(v).lower()
                if k in ("document_number", "date_of_birth", "expiry_date"):
                    if sv.replace("/", "").replace("-", "") in ocr_joined.replace("/", "").replace("-", "").replace(" ", ""):
                        fields.setdefault(k, None)
                        if fields.get(k) is None:
                            fields[k] = str(v)
                elif k == "full_name" and fields.get("full_name") is None:
                    fields[k] = str(v)
            vlm_used = True
        except Exception:
            vlm_used = False  # fail-closed: regex fields stand
    missing = [k for k in ("full_name", "date_of_birth", "document_number") if not fields.get(k)]
    return {"fields": fields, "missing_fields": missing,
            "ocr_confidence": ocr_out["avg_confidence"],
            "vlm_used": vlm_used, "ocr_lines": len(ocr_out["texts"])}


# ---------------------------------------------------------------- face match + liveness

def largest_face(det, img):
    h, w = img.shape[:2]
    det.setInputSize((w, h))
    _, faces = det.detect(img)
    if faces is None or len(faces) == 0:
        return None
    return max(faces, key=lambda f: f[2] * f[3])


def do_face_compare(id_image_b64, selfie_b64):
    det, rec = _load_face()
    id_img = b64_to_bgr(id_image_b64)
    sf_img = b64_to_bgr(selfie_b64)
    f1 = largest_face(det, id_img)
    f2 = largest_face(det, sf_img)
    if f1 is None or f2 is None:
        return {"matched": False, "similarity": 0.0,
                "reason": "face not detected in " + ("ID" if f1 is None else "selfie")}
    a = rec.feature(rec.alignCrop(id_img, f1))
    b = rec.feature(rec.alignCrop(sf_img, f2))
    sim = float(rec.match(a, b, 0))  # 0 = cosine distance metric
    return {"similarity": sim, "matched": sim >= 0.50,
            "threshold": 0.50, "faces_detected": 2}


def do_liveness(image_b64):
    import cv2
    import numpy as np
    nets = _load_liveness()
    det, _ = _load_face()
    img = b64_to_bgr(image_b64)
    f = largest_face(det, img)
    if f is None:
        return {"live": False, "score": 0.0, "reason": "no face detected"}
    h, w = img.shape[:2]
    x, y, bw, bh = f[0], f[1], f[2], f[3]
    probs_sum = np.zeros(3, dtype=np.float64)
    for sess, scale in nets:
        nw, nh = int(w * scale), int(h * scale)
        cx, cy = x + w / 2, y + bh / 2
        x1, y1 = max(0, int(cx - nw / 2)), max(0, int(cy - nh / 2))
        x2, y2 = min(w, int(cx + nw / 2)), min(h, int(cy + nh / 2))
        crop = img[y1:y2, x1:x2]
        if crop.size == 0:
            continue
        crop = cv2.resize(crop, (80, 80)).astype(np.float32) / 255.0
        crop = crop.transpose(2, 0, 1)[None, ...]
        out = sess.run(None, {sess.get_inputs()[0].name: crop})[0][0]
        e = np.exp(out - out.max())
        probs_sum += e / e.sum()
    probs = probs_sum / len(nets)
    label = int(probs.argmax())  # [spoof1, real, spoof2]
    real_prob = float(probs[1])
    return {"live": label == 1, "score": real_prob,
            "probs": [float(p) for p in probs],
            "model": "minifasnet_v1se_v2_ensemble"}


# ---------------------------------------------------------------- HTTP plumbing

class Handler(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def _send(self, code, obj):
        body = json.dumps(obj).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _body(self):
        n = int(self.headers.get("Content-Length", 0))
        if n > MAX_BODY:
            raise ValueError("body too large")
        return json.loads(self.rfile.read(n) or b"{}")

    def do_GET(self):
        if self.path == "/healthz":
            self._send(200, {"ok": True, "models_dir": MODELS_DIR,
                             "models_present": {
                                 "yunet": os.path.exists(os.path.join(MODELS_DIR, "face_detection_yunet_2023mar.onnx")),
                                 "sface": os.path.exists(os.path.join(MODELS_DIR, "face_recognition_sface_2021dec.onnx")),
                                 "minifasnet_v2": os.path.exists(os.path.join(MODELS_DIR, "MiniFASNetV2.onnx")),
                                 "minifasnet_v1se": os.path.exists(os.path.join(MODELS_DIR, "MiniFASNetV1SE.onnx")),
                             }})
            return
        self._send(404, {"ok": False, "error": "not found"})

    def do_POST(self):
        t0 = time.time()
        try:
            body = self._body()
            if self.path == "/ocr":
                out = do_ocr(body["image_b64"])
            elif self.path == "/parse":
                out = do_parse(body["doc_b64"], body.get("filename", "doc.pdf"))
            elif self.path == "/extract":
                out = do_extract(body["image_b64"], body.get("doc_type", ""))
            elif self.path == "/face/compare":
                out = do_face_compare(body["id_image_b64"], body["selfie_b64"])
            elif self.path == "/liveness":
                out = do_liveness(body["image_b64"])
            else:
                self._send(404, {"ok": False, "error": "not found"})
                return
            out["ok"] = True
            out["elapsed_ms"] = int((time.time() - t0) * 1000)
            self._send(200, out)
        except Exception as e:  # fail-closed
            traceback.print_exc(file=sys.stderr)
            self._send(200, {"ok": False, "error": f"{type(e).__name__}: {e}"})


if __name__ == "__main__":
    port = int(os.environ.get("PORT", "8105"))
    print(f"kyc_service listening on :{port} (models: {MODELS_DIR})", flush=True)
    ThreadingHTTPServer(("0.0.0.0", port), Handler).serve_forever()
