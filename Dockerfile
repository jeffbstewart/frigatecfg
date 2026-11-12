# Static binary in an empty image: the init container needs nothing
# else (the kubelet mounts the inputs and the state volume).
# Pinned by digest (golang:1.26.10, resolved 2026-10-26; 1.26.10 is the
# 2026-10-21 Go security release, crypto/x509 distrusting compromised
# intermediates, GO-2026-6809): the builder
# image controls the output binary, so it is pinned like a dependency.
FROM golang:1.26.11@sha256:6327cb290853a5d13091c436b88259b4f35ff70e49b1f8d9e5d1dd1757e9c6ba AS build
ENV GOFLAGS=-mod=readonly
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /frigatecfg .

FROM scratch
COPY --from=build /frigatecfg /frigatecfg
ENTRYPOINT ["/frigatecfg"]
