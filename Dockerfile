FROM alpine:3.21 AS builder

# Install build dependencies (using Go 1.24)
RUN apk add --no-cache \
    go~1.24 \
    gcc \
    g++ \
    pkgconf \
    vips-dev \
    build-base \
    glib-dev \
    glib-static \
    libheif-dev \
    vips-heif \
    libavif

# Set workdir
WORKDIR /build

# Copy source files
COPY go.mod main.go ./

# Initialize modules and download dependencies
RUN go mod tidy && \
    go get github.com/davidbyttow/govips/v2/vips && \
    go get github.com/fsnotify/fsnotify && \
    go get github.com/rs/zerolog

# Build the Go application - use non-static build due to vips dependencies
RUN CGO_ENABLED=1 CGO_CFLAGS="-I/usr/include" CGO_LDFLAGS="-L/usr/lib -lvips -lheif" GOOS=linux go build -o image-compressor .

# Create final smaller image
FROM alpine:3.21

# Install runtime dependencies
RUN apk add --no-cache \
    vips \
    ca-certificates \
    tini \
    glib \
    libheif \
    vips-heif \
    libavif

# Create a non-root user to run the application
RUN addgroup -S appgroup && adduser -S appuser -G appgroup

# Copy the compiled application from the builder stage
COPY --from=builder /build/image-compressor /usr/local/bin/

# Set working directory
WORKDIR /hedgedoc

# Use tini as entrypoint to handle signals properly
ENTRYPOINT ["/sbin/tini", "--"]

# Run the application
CMD ["/usr/local/bin/image-compressor"]
