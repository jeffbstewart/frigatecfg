# Static binary in an empty image: the init container needs nothing
# else (the kubelet mounts the inputs and the state volume).
FROM golang:1.25 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /frigatecfg .

FROM scratch
COPY --from=build /frigatecfg /frigatecfg
ENTRYPOINT ["/frigatecfg"]
