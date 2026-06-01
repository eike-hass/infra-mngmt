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
RUN mkdir -p /workspace /home/node/.claude /home/node/.cache /home/node/.config/gh && \
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

# Install Claude
RUN npm install -g @anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}

# Copy and set up privileged setup scripts
COPY init-firewall.sh /usr/local/bin/

USER root

RUN chmod +x /usr/local/bin/init-firewall.sh && \
  echo "node ALL=(root) NOPASSWD: /usr/local/bin/init-firewall.sh" > /etc/sudoers.d/node-setup && \
  chmod 0440 /etc/sudoers.d/node-setup

USER node
