FROM node:24

ARG TZ
ENV TZ="$TZ"

ARG CLAUDE_CODE_VERSION=latest

# Install basic development tools and iptables/ipset
RUN apt-get update && apt-get install -y --no-install-recommends \
  less \
  git \
  procps \
  sudo \
  fzf \
  zsh \
  man-db \
  unzip \
  gnupg2 \
  gh \
  iptables \
  ipset \
  iproute2 \
  dnsutils \
  aggregate \
  jq \
  nano \
  vim \
  wget \
  && apt-get clean && rm -rf /var/lib/apt/lists/*

# Install Go (wget is now guaranteed available from the apt block above).
# Keep this >= the `go` directive in go.mod, otherwise Go's auto-toolchain
# mechanism tries to fetch the matching version at every `go build`/`go test`,
# which fails inside this container: init-firewall.sh resolves and pins
# Google CDN IPs at startup, but proxy.golang.org's IPs rotate, so the
# subsequent toolchain download dies with "no route to host". Bumping this
# in lockstep with go.mod avoids the runtime fetch entirely.
ARG GO_VERSION=1.25.0
RUN ARCH="$(dpkg --print-architecture)" && \
  case "$ARCH" in \
    amd64) GOARCH=amd64 ;; \
    arm64) GOARCH=arm64 ;; \
    *) echo "Unsupported arch: $ARCH" && exit 1 ;; \
  esac && \
  wget -qO /tmp/go.tgz "https://go.dev/dl/go${GO_VERSION}.linux-${GOARCH}.tar.gz" && \
  tar -C /usr/local -xzf /tmp/go.tgz && \
  rm /tmp/go.tgz

ENV PATH=$PATH:/usr/local/go/bin
ENV GOPATH=/home/node/go
ENV GOBIN=/home/node/go/bin
ENV PATH=$PATH:/home/node/go/bin

# Install Playwright system-level OS dependencies for Chromium
RUN npx --yes playwright@1.52.0 install-deps chromium

# Install Docker CLI (no daemon) for docker-outside-of-docker. The daemon
# lives on the host; this container talks to it via the socket bind-mounted
# at /var/run/docker.sock. Static binary avoids needing Docker's apt repo.
ARG DOCKER_CLI_VERSION=27.5.1
RUN ARCH="$(uname -m)" && \
  case "$ARCH" in \
    x86_64) DOCKER_ARCH=x86_64 ;; \
    aarch64|arm64) DOCKER_ARCH=aarch64 ;; \
    *) echo "Unsupported arch: $ARCH" && exit 1 ;; \
  esac && \
  wget -qO /tmp/docker.tgz "https://download.docker.com/linux/static/stable/${DOCKER_ARCH}/docker-${DOCKER_CLI_VERSION}.tgz" && \
  tar -xzf /tmp/docker.tgz -C /tmp && \
  install -m 0755 /tmp/docker/docker /usr/local/bin/docker && \
  rm -rf /tmp/docker /tmp/docker.tgz

# Install golangci-lint from GitHub releases. The Go module proxy
# (proxy.golang.org via Google's CDN) rotates IPs, which can desync from the
# firewall's startup-time DNS resolution; GitHub releases have stable, allowed
# IP ranges so the install is reliable across rebuilds.
# v2.x is required because the project's transitive deps (OpenTelemetry pulled
# in via the Docker SDK) declare `go 1.25` in their go.mod files. v1.x of
# golangci-lint is built with Go 1.24 and rejects those deps with
# "package requires newer Go version go1.25". v2.12.1 is built with Go 1.26.
ARG GOLANGCI_LINT_VERSION=2.12.1
RUN ARCH="$(dpkg --print-architecture)" && \
  case "$ARCH" in \
    amd64) GOLANGCI_ARCH=linux-amd64 ;; \
    arm64) GOLANGCI_ARCH=linux-arm64 ;; \
    *) echo "Unsupported arch: $ARCH" && exit 1 ;; \
  esac && \
  wget -qO /tmp/golangci-lint.tgz \
    "https://github.com/golangci/golangci-lint/releases/download/v${GOLANGCI_LINT_VERSION}/golangci-lint-${GOLANGCI_LINT_VERSION}-${GOLANGCI_ARCH}.tar.gz" && \
  tar -xzf /tmp/golangci-lint.tgz -C /tmp && \
  install -m 0755 "/tmp/golangci-lint-${GOLANGCI_LINT_VERSION}-${GOLANGCI_ARCH}/golangci-lint" /usr/local/bin/golangci-lint && \
  rm -rf /tmp/golangci-lint.tgz "/tmp/golangci-lint-${GOLANGCI_LINT_VERSION}-${GOLANGCI_ARCH}"

# Install the Docker Compose CLI plugin alongside the docker binary so
# `docker compose ...` works. Direct binary from GitHub Releases —
# github.com is in the firewall allow-list. Lives in the system-wide
# cli-plugins dir so any user finds it without per-home setup.
ARG DOCKER_COMPOSE_VERSION=5.1.3
RUN ARCH="$(uname -m)" && \
  case "$ARCH" in \
    x86_64) COMPOSE_ARCH=x86_64 ;; \
    aarch64|arm64) COMPOSE_ARCH=aarch64 ;; \
    *) echo "Unsupported arch: $ARCH" && exit 1 ;; \
  esac && \
  mkdir -p /usr/local/lib/docker/cli-plugins && \
  wget -qO /usr/local/lib/docker/cli-plugins/docker-compose \
    "https://github.com/docker/compose/releases/download/v${DOCKER_COMPOSE_VERSION}/docker-compose-linux-${COMPOSE_ARCH}" && \
  chmod +x /usr/local/lib/docker/cli-plugins/docker-compose

# Placeholder docker group with arbitrary GID; setup-docker-group.sh aligns
# this to the socket's actual GID at postStart (it varies across hosts).
RUN groupadd -g 999 docker && usermod -aG docker node

# Ensure default node user has access to /usr/local/share
RUN mkdir -p /usr/local/share/npm-global && \
  chown -R node:node /usr/local/share

ARG USERNAME=node

# Persist bash history.
RUN SNIPPET="export PROMPT_COMMAND='history -a' && export HISTFILE=/commandhistory/.bash_history" \
  && mkdir /commandhistory \
  && touch /commandhistory/.bash_history \
  && chown -R $USERNAME /commandhistory

# Set `DEVCONTAINER` environment variable to help with orientation
ENV DEVCONTAINER=true

# Create workspace and config directories and set permissions.
# /home/node/.cache/ms-playwright is the mount-point for the playwright-browsers
# named volume; pre-creating it with node ownership means a freshly-created
# volume inherits those perms (Docker copies the mount-point dir into an empty
# volume on first attach).
RUN mkdir -p /workspace /home/node/.claude /home/node/.cache /home/node/.cache/ms-playwright /home/node/.config/gh && \
  chown -R node:node /workspace /home/node/.claude /home/node/.cache /home/node/.config/gh

WORKDIR /workspace

ARG GIT_DELTA_VERSION=0.18.2
RUN ARCH=$(dpkg --print-architecture) && \
  wget "https://github.com/dandavison/delta/releases/download/${GIT_DELTA_VERSION}/git-delta_${GIT_DELTA_VERSION}_${ARCH}.deb" && \
  sudo dpkg -i "git-delta_${GIT_DELTA_VERSION}_${ARCH}.deb" && \
  rm "git-delta_${GIT_DELTA_VERSION}_${ARCH}.deb"

# Set up non-root user
USER node

# Install global packages
ENV NPM_CONFIG_PREFIX=/usr/local/share/npm-global
ENV PATH=$PATH:/usr/local/share/npm-global/bin

# Set the default shell to zsh rather than sh
ENV SHELL=/bin/zsh

# Set the default editor and visual
ENV EDITOR=nano
ENV VISUAL=nano

# Default powerline10k theme
ARG ZSH_IN_DOCKER_VERSION=1.2.0
RUN sh -c "$(wget -O- https://github.com/deluan/zsh-in-docker/releases/download/v${ZSH_IN_DOCKER_VERSION}/zsh-in-docker.sh)" -- \
  -p git \
  -p fzf \
  -a "source /usr/share/doc/fzf/examples/key-bindings.zsh" \
  -a "source /usr/share/doc/fzf/examples/completion.zsh" \
  -a "export PROMPT_COMMAND='history -a' && export HISTFILE=/commandhistory/.bash_history" \
  -x

# Self-heal docker group membership in interactive shells. The host socket's
# GID is set at postStartCommand (groupmod), but terminals forked from the
# container init before that finished cache stale supplementary groups. If the
# socket's GID isn't in our group set, re-exec via newgrp to refresh it.
RUN cat >> /home/node/.zshrc <<'ZSHRC'

if [[ -S /var/run/docker.sock && -z "$_DOCKER_GROUP_CHECKED" ]]; then
  export _DOCKER_GROUP_CHECKED=1
  __sock_gid=$(stat -c '%g' /var/run/docker.sock 2>/dev/null)
  if [[ -n "$__sock_gid" ]] && ! id -G | tr ' ' '\n' | grep -qx "$__sock_gid"; then
    exec newgrp docker
  fi
  unset __sock_gid
fi
ZSHRC

# Install Claude
RUN npm install -g @anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}

# Pre-install the Chromium browser binary as the node user, so it lives in
# /home/node/.cache/ms-playwright (backed by the playwright-browsers volume).
# On first attach with a fresh volume, Docker seeds the volume from this path,
# making Chromium available without needing to download through the firewall.
RUN npx --yes playwright@1.52.0 install chromium

# Copy and set up privileged setup scripts
COPY init-firewall.sh /usr/local/bin/
COPY setup-docker-group.sh /usr/local/bin/

USER root

RUN chmod +x /usr/local/bin/init-firewall.sh /usr/local/bin/setup-docker-group.sh && \
  echo "node ALL=(root) NOPASSWD: /usr/local/bin/init-firewall.sh, /usr/local/bin/setup-docker-group.sh" > /etc/sudoers.d/node-setup && \
  chmod 0440 /etc/sudoers.d/node-setup

USER node
