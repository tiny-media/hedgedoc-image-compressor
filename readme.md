# HedgeDoc Image Compressor

HedgeDoc Image Compressor is a lightweight, efficient service designed to automatically optimize images uploaded to a HedgeDoc instance. It runs alongside your HedgeDoc container, monitoring for new image uploads and compressing them on-the-fly to reduce storage usage and improve load times.

### Features

- Automatic detection and compression of newly uploaded images
- Support for multiple output formats: WebP, AVIF, and JPEG
- Configurable compression settings including quality, dimensions, and performance parameters
- Intelligent compression that only replaces original images when significant size reduction is achieved
- Maintains original filenames for seamless integration with HedgeDoc
- Logging of compression processes and results

### How It Works

1. The service monitors the HedgeDoc uploads directory for new image files.
2. When a new image is detected, it's processed according to the configured settings.
3. The image is resized if it exceeds the maximum dimension setting.
4. The image is compressed and converted to the specified output format.
5. If the compressed version is smaller than the original by the specified threshold percentage, it replaces the original file.
6. The process is logged, including compression ratios and processing time.

### Configuration

The service can be customized using the following environment variables:

- `OUTPUT_FORMAT`: Desired output format (webp, avif, jpg). Default: avif
- `REDUCTION_THRESHOLD`: Minimum size reduction percentage to replace the original. Default: 15
- `WEBP_QUALITY`: WebP compression quality (1-100). Default: 70
- `AVIF_QUALITY`: AVIF compression quality (1-100). Default: 60
- `JPEG_QUALITY`: JPEG compression quality (1-100). Default: 75
- `MAX_DIMENSION`: Maximum width or height in pixels. Default: 1600
- `WATCH_DIR`: Directory to monitor for new images. Default: /hedgedoc/public/uploads
- `PARALLEL_PROCESSES`: Number of parallel compression processes. Default: 1
- `CPU_COUNT`: Number of CPUs to use. Default: 1
- `MALLOC_ARENA_MAX`: (Optional, System-Level) Setting this environment variable (e.g., `MALLOC_ARENA_MAX=2`) for the container can sometimes reduce memory usage by limiting memory arenas for underlying C libraries like `libvips`. It's not configured within the Go app itself. Default: System default.
- `DEBUG`: Set to true to enable debug mode for more verbose logging. Default: false

### Installation

1. Ensure you have a working HedgeDoc setup using Docker Compose.
2. Add the image-compress service to your `docker-compose.yml` file as shown in the provided example.
3. cd into the hedgedoc directory
4. Start the Hedgedoc Stack - an the first time the image-compress service will be built (building needs around 2min) 
   ```bash
   docker compose up -d
   ```

- You can delete/prune the build cache objects (around 1.3GB) after the image is built:
   ```bash
   docker builder prune
   ```
- The Container is around 100MB when built

### Monitoring

You can monitor the service using Docker logs:

```bash
docker logs hedgedoc-image-compress
```

### Technical Details

- Built with Go 1.23
- Uses libvips via go-vips for high-performance image processing (https://github.com/davidbyttow/govips/v2)
- Utilizes fsnotify for efficient file system monitoring (https://github.com/fsnotify/fsnotify)
- Implements zerolog for structured, leveled logging (https://github.com/rs/zerolog)

### Limitations

- Currently only processes new uploads, not existing images
- Designed for small to medium-sized installations, but parallel processing is supported
- AVIF encoding is much more ressource intensive then WebP for the cpu, but the results are even better with an even smaller file size

### Future Enhancements

- Maintenance mode for compressing existing images
- Database scanning for orphaned image cleanup

### Logs

If you encounter issues, check the Docker logs for error messages (e.g., `docker logs hedgedoc-image-compress`)


### Contributing

Contributions to improve the HedgeDoc Image Compressor are welcome. Please submit issues and pull requests on the project's GitHub repository.
