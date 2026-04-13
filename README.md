# S3-Viewer

A minimal Go reimplementation of [Clipface](https://github.com/tomsan/clipface).  
Media files are served directly from an S3 bucket (or any compatible storage: RustFS, Garage, VersityGW etc.).

## Features

- `/watch/<filename>` — media player page with Range request support (video seeking)
- Multi-format support: video, image, audio, PDF, text, JSON, download fallback
- OpenGraph + Twitter Card meta tags for embedding in Discord, Slack, Facebook, etc.
- Optional per-file title and description via S3 object metadata
- No authentication
- Single binary, zero runtime dependencies

## Configuration

All configuration is done via environment variables:

| Variable        | Description                                         | Default                  |
|-----------------|-----------------------------------------------------|--------------------------|
| `S3_BUCKET`     | S3 bucket name **(required)**                       | —                        |
| `S3_REGION`     | AWS region                                          | `us-east-1`              |
| `S3_ENDPOINT`   | Custom endpoint URL (MinIO, Garage…)                | *(AWS default)*          |
| `S3_ACCESS_KEY` | Access key                                          | *(AWS CLI credentials)*  |
| `S3_SECRET_KEY` | Secret key                                          | *(AWS CLI credentials)*  |
| `LISTEN_ADDR`   | Listen address                                      | `:8080`                  |
| `SITE_URL`      | Public base URL (used in meta tags, must be http(s))| `http://localhost:8080`  |
| `SITE_TITLE`    | Title displayed in the header                       | `Clipface`               |

## Run

```bash
# With AWS (credentials from ~/.aws/credentials)
S3_BUCKET=media SITE_URL=https://media.example.com go run .

# With local S3
S3_BUCKET=media \
S3_ENDPOINT=http://localhost:9000 \
S3_ACCESS_KEY=admin \
S3_SECRET_KEY=admin \
SITE_URL=http://localhost:8080 \
go run .
```

## Routes

| Route               | Description                                     |
|---------------------|-------------------------------------------------|
| `/<filename>` | Watch page (e.g. `/watch/my-clip.mp4`)          |
| `/e/<filename>`     | Raw media stream — supports `Range` requests    |

## Per-object metadata

Set a custom title or description on any clip via S3 object metadata:

```bash
aws s3 cp my-clip.mp4 s3://my-clips/my-clip.mp4 \
  --metadata 'title=My great clip,description=A short description'
```

## Build

```bash
go build -o clipface-go .
./clipface-go
```
