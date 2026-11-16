# ML services image: inference (ml-serve) + continuous training (ml-train).
# CPU-only PyTorch; registry mounted at /models (see docker-compose volumes).
FROM python:3.12-slim

WORKDIR /app
COPY ml/requirements.txt /app/ml/requirements.txt
RUN pip install --no-cache-dir torch --index-url https://download.pytorch.org/whl/cpu \
 && pip install --no-cache-dir -r ml/requirements.txt
COPY ml/ /app/ml/
ENV PYTHONPATH=/app/ml \
    ML_REGISTRY_ROOT=/models
EXPOSE 8077
CMD ["python", "-m", "pfa_ml.serve", "--port", "8077"]
