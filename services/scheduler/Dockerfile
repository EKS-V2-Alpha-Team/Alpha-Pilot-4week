FROM public.ecr.aws/docker/library/golang:1.27-alpine@sha256:b8c9b82affbbb82a020bbcf998dad2ba9516944ac7e883524642a481a9505e9c AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /app .

FROM gcr.io/distroless/static-debian13@sha256:15f8637509792e05b97d83451b0b6ccfa4abd548f997e406ac1e72c87fe1fa78
LABEL org.opencontainers.image.source="https://github.com/Jawwad78/Alpha-Pilot-4week" \
      org.opencontainers.image.title="scheduler" \
      org.opencontainers.image.description="Scheduled jobs cron-driven database maintenance and order cleanup" \
      org.opencontainers.image.base.name="gcr.io/distroless/static-debian13"
COPY --from=builder /app /app
USER nonroot:nonroot
EXPOSE 8091
CMD ["/app"]
