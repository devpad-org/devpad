FROM ubuntu:24.04

RUN apt-get update && apt-get install -y --no-install-recommends \
    bash \
    ca-certificates \
    curl \
    git \
    && rm -rf /var/lib/apt/lists/*

COPY devpad-agent /usr/local/bin/devpad-agent
RUN chmod +x /usr/local/bin/devpad-agent

RUN mkdir -p /workspace
WORKDIR /workspace

EXPOSE 9100

ENTRYPOINT ["/usr/local/bin/devpad-agent"]
