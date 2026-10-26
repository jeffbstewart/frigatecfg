# Static binary in an empty image: the init container needs nothing
# else (the kubelet mounts the inputs and the state volume).
# Pinned by digest (golang:1.26.10, resolved 2026-10-26; 1.26.10 is the
# 2026-10-21 Go security release, crypto/x509 distrusting compromised
# intermediates, GO-2026-6809): the builder
# image controls the output binary, so it is pinned like a dependency.
FROM golang:1.26.10@sha256:672c944cfd07599b5f1bb179869270cdc94c6254e41db4d431259693cb286b82 AS build
ENV GOFLAGS=-mod=readonly
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /frigatecfg .

FROM scratch
COPY --from=build /frigatecfg /frigatecfg
ENTRYPOINT ["/frigatecfg"]
