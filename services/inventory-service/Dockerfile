FROM public.ecr.aws/docker/library/golang:1.27-alpine@sha256:f92b6ef800e499660581efdabdf25d9d817a9d124eaf900924f0504e7e27e12d AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /app .

FROM gcr.io/distroless/static-debian13@sha256:58133991db06659feaabe0f4e97a35cebf15ef4ea08f8a4c6d2ee5f75e4aa6a0
LABEL org.opencontainers.image.source="https://github.com/Jawwad78/Alpha-Pilot-4week" \
      org.opencontainers.image.title="inventory-service" \
      org.opencontainers.image.description="Inventory management stock levels, reservations, and product catalog" \
      org.opencontainers.image.base.name="gcr.io/distroless/static-debian13"
COPY --from=builder /app /app
USER nonroot:nonroot
EXPOSE 8082
CMD ["/app"]
