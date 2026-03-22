FROM python:3.12-slim
WORKDIR /app
RUN pip install --no-cache-dir setuptools
COPY mock-akamai-edge/requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY shared/otel.py ./otel.py
COPY mock-akamai-edge/ .
CMD ["python", "main.py"]
