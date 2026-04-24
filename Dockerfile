# Build stage
FROM registry.suse.com/bci/golang:latest AS build

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o etcd-reliability-tool ./cmd/etcd-reliability-tool

# Final stage - SLES BCI Micro is the SLES equivalent of distroless
FROM registry.suse.com/bci/bci-micro:latest

WORKDIR /

COPY --from=build /app/etcd-reliability-tool /etcd-reliability-tool

# Use numeric ID for security context, as bci-micro is minimal
USER 65532:65532

ENTRYPOINT ["/etcd-reliability-tool"]
