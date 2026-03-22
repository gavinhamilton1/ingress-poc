FROM envoyproxy/envoy:v1.30-latest
RUN apt-get update && apt-get install -y --no-install-recommends gettext-base && rm -rf /var/lib/apt/lists/*
COPY envoy-template.yaml /etc/envoy/envoy-template.yaml
COPY docker-entrypoint.sh /docker-entrypoint.sh
RUN chmod +x /docker-entrypoint.sh
EXPOSE 8000 9901
ENTRYPOINT ["/docker-entrypoint.sh"]
