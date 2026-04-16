FROM debian:trixie-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    bash \
    ca-certificates \
    curl \
    git \
    sudo \
    tmux \
    && rm -rf /var/lib/apt/lists/*

RUN groupadd --gid 1000 devpad \
    && useradd --uid 1000 --gid 1000 --create-home --shell /bin/bash devpad \
    && echo 'devpad ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/devpad \
    && chmod 0440 /etc/sudoers.d/devpad

COPY devpad-agent /usr/local/bin/devpad-agent
RUN chmod +x /usr/local/bin/devpad-agent

RUN mkdir -p /workspace && chown devpad:devpad /workspace
WORKDIR /workspace

USER devpad

EXPOSE 9100

ENTRYPOINT ["/usr/local/bin/devpad-agent"]
