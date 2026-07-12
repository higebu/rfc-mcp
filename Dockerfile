# 1) Build the static binary.
FROM golang:1.26-bookworm AS go-builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w" \
    -o /rfc-mcp ./cmd/rfc-mcp

# 2) Build the database. By default the full RFC corpus (~9800 RFCs, ~8 min,
#    ~865 MB) is baked in; pass --build-arg FROM_RFC=9290 --build-arg TO_RFC=9295
#    to restrict to a numeric range for a fast smoke-test image. No LibreOffice
#    needed here (plain-text parsing only, unlike 3gpp-mcp's .docx pipeline) --
#    just ca-certificates for the HTTPS fetches to rfc-editor.org.
FROM golang:1.26-bookworm AS db-builder
ARG FROM_RFC=
ARG TO_RFC=
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY --from=go-builder /rfc-mcp /rfc-mcp
RUN FROM_FLAG="" \
    && TO_FLAG="" \
    && if [ -n "${FROM_RFC}" ]; then FROM_FLAG="--from ${FROM_RFC}"; fi \
    && if [ -n "${TO_RFC}" ]; then TO_FLAG="--to ${TO_RFC}"; fi \
    && /rfc-mcp build ${FROM_FLAG} ${TO_FLAG} --db /rfc.db

# 3) Final image: just the binary, the baked-in database, and CA certs.
FROM scratch
COPY --from=db-builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=go-builder /rfc-mcp /rfc-mcp
COPY --from=db-builder /rfc.db /rfc.db
ENTRYPOINT ["/rfc-mcp"]
CMD ["serve", "--db", "/rfc.db"]
