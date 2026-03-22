FROM openpolicyagent/opa:latest
COPY policies/ /policies/
COPY opa-config.yaml /opa-config.yaml
EXPOSE 8181
CMD ["run", "--server", "--addr", "0.0.0.0:8181", "--config-file", "/opa-config.yaml", "/policies"]
