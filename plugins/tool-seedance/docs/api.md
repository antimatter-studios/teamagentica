# Seedance 2.0 API Documentation

Base URL: `https://seedanceapi.org/v1`

## Authentication

All API requests require authentication using a Bearer token in the Authorization header.

```
Authorization: Bearer YOUR_API_KEY
```

## Pricing

### 480p Resolution

Fast generation, suitable for previews and drafts.

| Duration | Without Audio | With Audio |
|----------|--------------|------------|
| 4s | 8 credits ($0.04) | 14 credits ($0.07) |
| 8s | 14 credits ($0.07) | 28 credits ($0.14) |
| 12s | 19 credits ($0.095) | 38 credits ($0.19) |

### 720p Resolution

High quality output, recommended for production.

| Duration | Without Audio | With Audio |
|----------|--------------|------------|
| 4s | 14 credits ($0.07) | 28 credits ($0.14) |
| 8s | 28 credits ($0.14) | 56 credits ($0.28) |
| 12s | 42 credits ($0.21) | 84 credits ($0.42) |

## API Endpoints

### POST `/v1/generate`

Create a new video generation task. Supports text-to-video and image-to-video modes.

#### Request Body

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `prompt` | string | Yes | Text description of the video to generate (max 2000 characters) |
| `aspect_ratio` | string | No | Output aspect ratio. Supported: `1:1`, `16:9`, `9:16`, `4:3`, `3:4`, `21:9`, `9:21`. Defaults to `1:1`. |
| `resolution` | string | No | Video resolution: `480p` or `720p`. Defaults to `720p`. |
| `duration` | string | No | Video duration in seconds: `4`, `8`, or `12`. Defaults to `8`. |
| `generate_audio` | boolean | No | Enable AI audio generation for the video. Defaults to `false`. |
| `fixed_lens` | boolean | No | Lock the camera lens to reduce motion blur. Defaults to `false`. |
| `image_urls` | string[] | No | Array of reference image URLs for image-to-video generation (max 1 image) |
| `callback_url` | string | No | Webhook URL for async status notifications. Must be publicly accessible (no localhost). |

#### Text to Video Example

```json
{
  "prompt": "A majestic eagle soaring through golden sunset clouds over ocean waves",
  "aspect_ratio": "16:9",
  "resolution": "720p",
  "duration": "8"
}
```

#### Image to Video Example

```json
{
  "prompt": "The character slowly turns and smiles at the camera",
  "image_urls": [
    "https://example.com/my-image.jpg"
  ],
  "aspect_ratio": "16:9",
  "resolution": "720p",
  "duration": "4"
}
```

#### With Audio Generation

```json
{
  "prompt": "A peaceful river flowing through a forest with birds singing",
  "aspect_ratio": "16:9",
  "resolution": "720p",
  "duration": "8",
  "generate_audio": true,
  "fixed_lens": true
}
```

#### Response (200 Success)

```json
{
  "code": 200,
  "message": "success",
  "data": {
    "task_id": "seed15abc123def456pro",
    "status": "IN_PROGRESS"
  }
}
```

### GET `/v1/status`

Check the status of a video generation task and retrieve the result when completed.

#### Query Parameters

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `task_id` | string | Yes | The unique task ID returned from the generate endpoint |

#### Example Request

```bash
curl -X GET 'https://seedanceapi.org/v1/status?task_id=seed15abc123def456pro' \
  -H 'Authorization: Bearer YOUR_API_KEY'
```

**Tip:** The `response` field in the status API is an array of video URLs. Access `data.response[0]` to get the video URL.

#### Response (200 Completed)

```json
{
  "code": 200,
  "message": "success",
  "data": {
    "task_id": "seed15abc123def456pro",
    "status": "SUCCESS",
    "consumed_credits": 28,
    "created_at": "2026-02-07T10:30:00Z",
    "request": {
      "prompt": "A majestic eagle soaring through golden sunset clouds",
      "aspect_ratio": "16:9",
      "resolution": "720p",
      "duration": "8"
    },
    "response": [
      "https://cdn.example.com/videos/seed15abc123def456pro.mp4"
    ],
    "error_message": null
  }
}
```

#### Response (200 Processing)

Status will be `"IN_PROGRESS"` while the video is being generated.

#### Response (200 Failed)

Status will be `"FAILED"` with `error_message` populated.

## Error Codes

| Status | Code | Description |
|--------|------|-------------|
| 400 | `INVALID_PROMPT` | The prompt is invalid or empty |
| 400 | `INVALID_ASPECT_RATIO` | Unsupported aspect ratio value |
| 400 | `INVALID_RESOLUTION` | Resolution must be 480p or 720p |
| 400 | `INVALID_DURATION` | Duration must be 4, 8, or 12 seconds |
| 400 | `TOO_MANY_IMAGES` | Maximum 1 image URL allowed in image_urls array |
| 401 | `INVALID_API_KEY` | API key is missing or invalid |
| 402 | `INSUFFICIENT_CREDITS` | Not enough credits for this operation |
| 404 | `TASK_NOT_FOUND` | Task ID not found or does not belong to your account |
| 500 | `INTERNAL_ERROR` | Server error, please try again later |
