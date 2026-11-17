# kyc-service model weights

| File | Source | License | Purpose |
|---|---|---|---|
| face_detection_yunet_2023mar.onnx | opencv/opencv_zoo (main, LFS) | Apache-2.0 | YuNet face detection |
| face_recognition_sface_2021dec.onnx | opencv/opencv_zoo (main, LFS) | Apache-2.0 | SFace embeddings, cosine match (threshold 0.50) |
| MiniFASNetV2.onnx | QingHeYang/Silent-Face-Anti-Spoofing-onnx (`onnx/2.7_80x80_MiniFASNetV2.onnx`) | Apache-2.0 | Liveness, crop scale 2.7 |
| MiniFASNetV1SE.onnx | QingHeYang/Silent-Face-Anti-Spoofing-onnx (`onnx/4_0_0_80x80_MiniFASNetV1SE.onnx`) | Apache-2.0 | Liveness, crop scale 4.0 |

Note: insightface buffalo_l weights are non-commercial and are
deliberately NOT used. PaddleOCR PP-OCRv5 mobile weights are downloaded
by the `paddleocr` package on first run (Apache-2.0).
