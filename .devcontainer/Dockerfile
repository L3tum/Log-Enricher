FROM golang:1.24-bookworm

ARG STATICCHECK_VERSION=v0.6.1

# Install common CLI tools expected in a development container.
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        bash-completion \
        ca-certificates \
        git \
        openssh-client \
    && GOBIN=/usr/local/bin go install honnef.co/go/tools/cmd/staticcheck@${STATICCHECK_VERSION} \
    && rm -rf /var/lib/apt/lists/*
