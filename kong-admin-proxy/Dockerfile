FROM python:3.12-slim
WORKDIR /app
RUN pip install --no-cache-dir setuptools
COPY kong-admin-proxy/requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY shared/otel.py ./otel.py
COPY kong-admin-proxy/ .
CMD ["python", "main.py"]
