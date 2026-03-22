#!/bin/sh
envsubst '${XDS_HOST} ${XDS_PORT}' < /etc/envoy/envoy-template.yaml > /etc/envoy/envoy.yaml
exec envoy -c /etc/envoy/envoy.yaml "$@"
