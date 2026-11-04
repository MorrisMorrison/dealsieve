FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# The release tag (v0.0.1) or dev-<sha>, set by the publish workflow.
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags="-s -w -X github.com/MorrisMorrison/dealsieve/internal/version.Version=${VERSION}" \
    -o /out/dealsieve ./cmd/dealsieve && \
    mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/dealsieve /dealsieve
# A named volume copies this directory's owner, so the nonroot user can write.
COPY --from=build --chown=65532:65532 /out/data /data
ENV DEALSIEVE_DATA_DIR=/data \
    DEALSIEVE_LISTEN=:8080
VOLUME /data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s CMD ["/dealsieve", "healthcheck"]
ENTRYPOINT ["/dealsieve"]
