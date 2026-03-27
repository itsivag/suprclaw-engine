FROM public.ecr.aws/docker/library/golang:1.25-bookworm AS builder

RUN apt-get update && apt-get install -y --no-install-recommends git ca-certificates make && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY . .
RUN make build

FROM public.ecr.aws/docker/library/ubuntu:24.04

ENV DEBIAN_FRONTEND=noninteractive
ENV SUPRCLAW_HOME=/home/suprclaw/.suprclaw
ENV SUPRCLAW_CONFIG=/home/suprclaw/.suprclaw/config.json
ENV SUPRCLAW_ENGINE_HOME=/home/suprclaw/.suprclaw
ENV SUPRCLAW_ENGINE_CONFIG=/home/suprclaw/.suprclaw/config.json
ENV PICOCLAW_HOME=/home/suprclaw/.suprclaw
ENV PICOCLAW_CONFIG=/home/suprclaw/.suprclaw/config.json

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

RUN useradd -m -s /bin/bash suprclaw && mkdir -p /home/suprclaw/.suprclaw \
    && groupadd -f suprclaw \
    && usermod -aG suprclaw suprclaw

COPY --from=builder /src/build/suprclaw /usr/local/bin/suprclaw
COPY docker/runtime/entrypoint.sh /entrypoint.sh
COPY docker/runtime/supervisord.conf /etc/supervisor/conf.d/supervisord.conf
COPY --chown=suprclaw:suprclaw docker/runtime/workspace/ /opt/suprclaw/workspace/

RUN chmod 755 /entrypoint.sh /usr/local/bin/suprclaw

EXPOSE 18790

ENTRYPOINT ["/usr/bin/tini", "--", "/entrypoint.sh"]
