# Static binary in an empty image: the init container needs nothing
# else (the kubelet mounts the inputs and the state volume).
# Pinned by digest (golang:1.26.9, resolved 2026-10-09; 1.26.9 carries the
# net/http fix for GO-2026-6617): the builder
# image controls the output binary, so it is pinned like a dependency.
FROM golang:1.26.9@sha256:f1f0bcc2c524a3ced375fcb4d1ecb7aa371aa7070e112599aaca45cc02d0101b AS build
ENV GOFLAGS=-mod=readonly
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /frigatecfg .

FROM scratch
COPY --from=build /frigatecfg /frigatecfg
ENTRYPOINT ["/frigatecfg"]
