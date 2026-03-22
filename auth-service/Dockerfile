FROM python:3.12-slim
WORKDIR /app
RUN pip install --no-cache-dir setuptools
COPY auth-service/requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY shared/otel.py ./otel.py
COPY auth-service/ .
CMD ["python", "main.py"]
