# kyc-service: next-generation document intelligence + biometrics sidecar.
# PaddleOCR (PP-OCRv5 mobile) + Docling for document intelligence,
# YuNet + SFace (OpenCV Zoo, Apache-2.0) for face match,
# MiniFASNetV1SE+V2 ensemble (Silent-Face-Anti-Spoofing, Apache-2.0) for
# passive liveness. Optional VLM cross-validation via ollama (OLLAMA_URL).
# Weights are vendored in ml/kyc_service/weights/ (see weights/README.md).
FROM python:3.12-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
      libgl1 libglib2.0-0 \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY ml/kyc_service/requirements.txt /app/requirements.txt
RUN pip install --no-cache-dir -r requirements.txt
COPY ml/kyc_service/ /app/

ENV PORT=8105 \
    KYC_MODELS_DIR=/app/weights
EXPOSE 8105
HEALTHCHECK --interval=30s --timeout=5s --retries=3 \
  CMD python -c "import urllib.request,os;urllib.request.urlopen('http://127.0.0.1:'+os.environ.get('PORT','8105')+'/healthz',timeout=4)"
CMD ["python", "app.py"]
