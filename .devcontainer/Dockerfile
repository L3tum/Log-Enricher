FROM golang:1.24-bookworm

# Install common CLI tools expected in a development container.
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        bash-completion \
        ca-certificates \
        git \
        openssh-client \
    && rm -rf /var/lib/apt/lists/*
