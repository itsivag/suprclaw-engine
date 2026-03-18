FROM docker.io/library/golang:1.25-bookworm AS builder

RUN apt-get update && apt-get install -y --no-install-recommends git ca-certificates make && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY . .
RUN make build

FROM docker.io/library/ubuntu:24.04

ENV DEBIAN_FRONTEND=noninteractive
ENV SUPRCLAW_HOME=/home/picoclaw/.suprclaw
ENV SUPRCLAW_CONFIG=/home/picoclaw/.suprclaw/config.json
ENV SUPRCLAW_ENGINE_HOME=/home/picoclaw/.suprclaw
ENV SUPRCLAW_ENGINE_CONFIG=/home/picoclaw/.suprclaw/config.json
ENV PICOCLAW_HOME=/home/picoclaw/.suprclaw
ENV PICOCLAW_CONFIG=/home/picoclaw/.suprclaw/config.json

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    git \
    jq \
    nodejs \
    npm \
    openssh-client \
    python3 \
    supervisor \
    tini \
    && rm -rf /var/lib/apt/lists/*

RUN useradd -m -s /bin/bash picoclaw && mkdir -p /home/picoclaw/.suprclaw \
    && groupadd -f picoclaw \
    && usermod -aG picoclaw picoclaw

COPY --from=builder /src/build/suprclaw /usr/local/bin/suprclaw
COPY docker/runtime/entrypoint.sh /entrypoint.sh
COPY docker/runtime/supervisord.conf /etc/supervisor/conf.d/supervisord.conf
COPY --chown=picoclaw:picoclaw docker/runtime/workspace/ /opt/suprclaw/workspace/

RUN chmod 755 /entrypoint.sh /usr/local/bin/suprclaw

EXPOSE 18790

ENTRYPOINT ["/usr/bin/tini", "--", "/entrypoint.sh"]
