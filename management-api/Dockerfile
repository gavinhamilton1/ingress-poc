FROM python:3.12-slim
WORKDIR /app
RUN pip install --no-cache-dir setuptools
COPY management-api/requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY shared/otel.py ./otel.py
COPY management-api/ .
CMD ["python", "main.py"]
