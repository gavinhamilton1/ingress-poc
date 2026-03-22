FROM coredns/coredns:latest
COPY Corefile /etc/coredns/Corefile
COPY jpm.com.db /etc/coredns/jpm.com.db
EXPOSE 53 53/udp
CMD ["-conf", "/etc/coredns/Corefile"]
