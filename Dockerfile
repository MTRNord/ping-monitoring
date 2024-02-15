FROM golang:1.22 AS build-stage

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY *.go ./
# Build the Go binary with version information
ARG VERSION
ARG BRANCH
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags "-X github.com/prometheus/common/version.Version=${VERSION} -X github.com/prometheus/common/version.Branch=${BRANCH}" \
    -o /ping-monitoring

FROM gcr.io/distroless/base-debian12

WORKDIR /

COPY --from=build-stage /ping-monitoring /ping-monitoring

EXPOSE 8080

USER nonroot:nonroot

ENTRYPOINT ["/ping-monitoring"]
