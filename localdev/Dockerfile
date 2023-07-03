FROM ubuntu as server

RUN apt update && apt install -y ca-certificates curl git

WORKDIR /tmp
RUN curl -fsSL "https://raw.githubusercontent.com/kubernetes-sigs/kustomize/master/hack/install_kustomize.sh" -o install_kustomize.sh && \
    chmod 700 install_kustomize.sh && \ 
    ./install_kustomize.sh 4.5.7 /usr/local/bin
RUN curl -fsSL  "https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3" -o get-helm-3.sh && \
    chmod 700 get-helm-3.sh && \ 
    ./get-helm-3.sh -v v3.10.0

WORKDIR /app
COPY build/kubechecks /app/kubechecks
COPY ../policy /app/policy
COPY ../schemas /app/schemas

CMD ["/app/kubechecks", "controller"]
