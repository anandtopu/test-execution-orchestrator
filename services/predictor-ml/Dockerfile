FROM python:3.12-slim

ENV PYTHONUNBUFFERED=1 \
    PIP_NO_CACHE_DIR=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1

RUN apt-get update && apt-get install -y --no-install-recommends \
        build-essential libgomp1 \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY pyproject.toml ./
COPY src ./src

RUN pip install --upgrade pip wheel \
    && pip install -e .

EXPOSE 8000
USER 65534:65534

ENTRYPOINT ["uvicorn", "teo_predictor_ml.app:app", "--host", "0.0.0.0", "--port", "8000"]
