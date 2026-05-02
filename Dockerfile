# Multi-stage Dockerfile that builds any TEO Go service.
#   docker build --build-arg SERVICE=api -t teo/api .

ARG GO_VERSION=1.23

FROM golang:${GO_VERSION}-alpine AS builder
ARG SERVICE
ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown

RUN apk add --no-cache git ca-certificates

WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download || true
COPY . .

RUN CGO_ENABLED=0 GOOS=linux \
    go build \
      -trimpath \
      -ldflags "-s -w \
        -X github.com/teo-dev/teo/internal/version.Version=${VERSION} \
        -X github.com/teo-dev/teo/internal/version.Commit=${COMMIT} \
        -X github.com/teo-dev/teo/internal/version.Date=${DATE}" \
      -o /out/service \
      ./cmd/${SERVICE}

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/service /usr/local/bin/service
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/service"]
