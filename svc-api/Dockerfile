FROM python:3.12-slim
WORKDIR /app
RUN pip install --no-cache-dir setuptools
COPY svc-api/requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY shared/otel.py ./otel.py
COPY svc-api/ .
CMD ["python", "main.py"]
