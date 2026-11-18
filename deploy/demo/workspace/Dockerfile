ARG DOCKER_IMAGE=docker:29.9.3-dind@sha256:08a98015fce594af7c382883bea080ea206de74402a95da9dc52b6428c8cd121
ARG WORKSPACE_IMAGE=blazux/prism-workspace:demo-cpu-base-20261112
FROM ${DOCKER_IMAGE} AS docker-binaries
FROM ${WORKSPACE_IMAGE}

USER root
RUN apt-get update -qq \
    && apt-get install -y --no-install-recommends iptables iproute2 kmod pigz xz-utils \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /usr/local/sbin/.iptables-legacy \
    && for name in iptables iptables-save iptables-restore ip6tables ip6tables-save ip6tables-restore; do \
         legacy="$(printf '%s' "$name" | sed 's/tables/tables-legacy/')"; \
         ln -s "$(command -v "$legacy")" "/usr/local/sbin/.iptables-legacy/$name"; \
       done

COPY --from=docker-binaries /usr/local/bin/ /usr/local/bin/
COPY --from=docker-binaries /usr/local/libexec/docker/cli-plugins/ /usr/local/libexec/docker/cli-plugins/
COPY daemon.json /etc/docker/daemon.json
COPY entrypoint.sh /usr/local/bin/prism-demo-workspace
RUN chmod 0755 /usr/local/bin/prism-demo-workspace \
    && docker --version && dockerd --version && docker compose version && docker buildx version \
    && dockerd --validate --config-file=/etc/docker/daemon.json

ENV DOCKER_HOST=unix:///var/run/docker.sock
ENV DOCKER_TLS_CERTDIR=
VOLUME /var/lib/docker
WORKDIR /workspace
HEALTHCHECK --interval=15s --timeout=5s --start-period=60s --retries=3 CMD docker info >/dev/null 2>&1 || exit 1
ENTRYPOINT ["/usr/local/bin/prism-demo-workspace"]
CMD []
