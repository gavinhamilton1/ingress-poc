FROM python:3.12-slim
WORKDIR /app
RUN pip install --no-cache-dir setuptools
COPY mock-psaas/requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY shared/otel.py ./otel.py
COPY mock-psaas/ .
CMD ["python", "main.py"]
