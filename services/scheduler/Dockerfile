FROM public.ecr.aws/docker/library/golang:1.27-alpine@sha256:738d1cf061836894ff6bb8c33881080ac66de8cf0586615012a0c8f592649cfa AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /app .

FROM gcr.io/distroless/static-debian13@sha256:58133991db06659feaabe0f4e97a35cebf15ef4ea08f8a4c6d2ee5f75e4aa6a0
LABEL org.opencontainers.image.source="https://github.com/Jawwad78/Alpha-Pilot-4week" \
      org.opencontainers.image.title="scheduler" \
      org.opencontainers.image.description="Scheduled jobs cron-driven database maintenance and order cleanup" \
      org.opencontainers.image.base.name="gcr.io/distroless/static-debian13"
COPY --from=builder /app /app
USER nonroot:nonroot
EXPOSE 8091
CMD ["/app"]
