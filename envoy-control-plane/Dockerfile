FROM python:3.12-slim
WORKDIR /app
RUN pip install --no-cache-dir setuptools
COPY envoy-control-plane/requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY shared/otel.py ./otel.py
COPY envoy-control-plane/ .
CMD ["python", "main.py"]
