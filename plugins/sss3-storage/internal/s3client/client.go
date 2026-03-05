package s3client

import (
	"context"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/antimatter-studios/teamagentica/plugins/sss3-storage/internal/config"
)

// ObjectMeta holds metadata about a stored object.
type ObjectMeta struct {
	Key          string    `json:"key"`
	Size         int64     `json:"size"`
	ContentType  string    `json:"content_type"`
	LastModified time.Time `json:"last_modified"`
	ETag         string    `json:"etag"`
}

// Client wraps the AWS S3 SDK for communicating with SSS3.
type Client struct {
	s3       *s3.Client
	uploader *manager.Uploader
	bucket   string
	debug    bool
}

// New creates a new S3 client configured for the local sss3 sidecar.
func New(cfg *config.Config) *Client {
	localEndpoint := fmt.Sprintf("http://localhost:%d", cfg.S3Port)
	s3Client := s3.New(s3.Options{
		BaseEndpoint: aws.String(localEndpoint),
		Region:       cfg.S3Region,
		Credentials:  credentials.NewStaticCredentialsProvider(cfg.S3AccessKey, cfg.S3SecretKey, ""),
		UsePathStyle: true,
	})

	return &Client{
		s3:       s3Client,
		uploader: manager.NewUploader(s3Client),
		bucket:   cfg.S3Bucket,
		debug:    cfg.Debug,
	}
}

// EnsureBucket creates the bucket if it doesn't exist.
func (c *Client) EnsureBucket(ctx context.Context) error {
	_, err := c.s3.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(c.bucket),
	})
	if err == nil {
		if c.debug {
			log.Printf("[s3] bucket %s already exists", c.bucket)
		}
		return nil
	}

	_, err = c.s3.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(c.bucket),
	})
	if err != nil {
		return fmt.Errorf("create bucket %s: %w", c.bucket, err)
	}
	log.Printf("[s3] created bucket %s", c.bucket)
	return nil
}

// PutObject uploads an object to S3 using multipart upload.
// The manager reads the body in ~5MB chunks, hashing each part individually,
// so the full body never needs to be buffered or seeked.
func (c *Client) PutObject(ctx context.Context, key string, body io.Reader, contentType string) error {
	input := &s3.PutObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
		Body:   body,
	}
	if contentType != "" {
		input.ContentType = aws.String(contentType)
	}

	_, err := c.uploader.Upload(ctx, input)
	if err != nil {
		return fmt.Errorf("put object %s: %w", key, err)
	}
	if c.debug {
		log.Printf("[s3] put %s", key)
	}
	return nil
}

// GetObjectOutput holds the response from GetObject.
type GetObjectOutput struct {
	Body        io.ReadCloser
	ContentType string
	Size        int64
	ETag        string
}

// GetObject retrieves an object from S3.
func (c *Client) GetObject(ctx context.Context, key string) (*GetObjectOutput, error) {
	out, err := c.s3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("get object %s: %w", key, err)
	}

	ct := ""
	if out.ContentType != nil {
		ct = *out.ContentType
	}
	etag := ""
	if out.ETag != nil {
		etag = *out.ETag
	}
	sz := int64(0)
	if out.ContentLength != nil {
		sz = *out.ContentLength
	}

	return &GetObjectOutput{
		Body:        out.Body,
		ContentType: ct,
		Size:        sz,
		ETag:        etag,
	}, nil
}

// DeleteObject removes an object from S3.
func (c *Client) DeleteObject(ctx context.Context, key string) error {
	_, err := c.s3.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("delete object %s: %w", key, err)
	}
	if c.debug {
		log.Printf("[s3] deleted %s", key)
	}
	return nil
}

// HeadObject retrieves metadata for an object without downloading it.
func (c *Client) HeadObject(ctx context.Context, key string) (*ObjectMeta, error) {
	out, err := c.s3.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("head object %s: %w", key, err)
	}

	ct := ""
	if out.ContentType != nil {
		ct = *out.ContentType
	}
	etag := ""
	if out.ETag != nil {
		etag = *out.ETag
	}
	sz := int64(0)
	if out.ContentLength != nil {
		sz = *out.ContentLength
	}
	lm := time.Time{}
	if out.LastModified != nil {
		lm = *out.LastModified
	}

	return &ObjectMeta{
		Key:          key,
		Size:         sz,
		ContentType:  ct,
		LastModified: lm,
		ETag:         etag,
	}, nil
}

// ListObjects returns all objects with the given prefix.
func (c *Client) ListObjects(ctx context.Context, prefix string) ([]ObjectMeta, error) {
	var objects []ObjectMeta

	paginator := s3.NewListObjectsV2Paginator(c.s3, &s3.ListObjectsV2Input{
		Bucket: aws.String(c.bucket),
		Prefix: aws.String(prefix),
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list objects prefix=%s: %w", prefix, err)
		}
		for _, obj := range page.Contents {
			objects = append(objects, objectToMeta(obj))
		}
	}

	if c.debug {
		log.Printf("[s3] listed %d objects with prefix %q", len(objects), prefix)
	}
	return objects, nil
}

func objectToMeta(obj types.Object) ObjectMeta {
	key := ""
	if obj.Key != nil {
		key = *obj.Key
	}
	etag := ""
	if obj.ETag != nil {
		etag = *obj.ETag
	}
	lm := time.Time{}
	if obj.LastModified != nil {
		lm = *obj.LastModified
	}
	sz := int64(0)
	if obj.Size != nil {
		sz = *obj.Size
	}
	return ObjectMeta{
		Key:          key,
		Size:         sz,
		LastModified: lm,
		ETag:         etag,
	}
}
