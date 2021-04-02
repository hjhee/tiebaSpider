# See here for image contents: https://github.com/microsoft/vscode-dev-containers/tree/v0.163.1/containers/go/.devcontainer/base.Dockerfile

# [Choice] Go version: 1, 1.16, 1.15
ARG VARIANT="1.16"
FROM mcr.microsoft.com/vscode/devcontainers/go:0-${VARIANT}

# [Option] Install Node.js
ARG INSTALL_NODE="true"
ARG NODE_VERSION="lts/*"
RUN if [ "${INSTALL_NODE}" = "true" ]; then su vscode -c "umask 0002 && . /usr/local/share/nvm/nvm.sh && nvm install ${NODE_VERSION} 2>&1"; fi

# [Optional] Uncomment this section to install additional OS packages.
# RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
#     && apt-get -y install --no-install-recommends <your-package-list-here>

# [Optional] Uncomment the next line to use go get to install anything else you need
RUN su vscode -c "go get -x github.com/PuerkitoBio/goquery"
RUN su vscode -c "GOPROXY=https://proxy.golang.org GO111MODULE=on go get golang.org/x/tools/gopls@latest"

# [Optional] Uncomment this line to install global node packages.
# RUN su vscode -c "source /usr/local/share/nvm/nvm.sh && npm install -g <your-package-here>" 2>&1

# debug github actions locally
# ref: https://github.com/nektos/act
RUN curl https://raw.githubusercontent.com/nektos/act/master/install.sh | bash
