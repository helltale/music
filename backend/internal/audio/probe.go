package audio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

type mediaInfo struct {
	Codec      string
	SampleRate int
	Channels   int
	DurationMS int
	Bitrate    int
	SizeBytes  int64
}

func probeFile(ctx context.Context, path string) (mediaInfo, error) {
	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		path,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return mediaInfo{}, fmt.Errorf("ffprobe: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var parsed struct {
		Streams []struct {
			CodecType  string `json:"codec_type"`
			CodecName  string `json:"codec_name"`
			SampleRate string `json:"sample_rate"`
			Channels   int    `json:"channels"`
			Duration   string `json:"duration"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
			Size     string `json:"size"`
			BitRate  string `json:"bit_rate"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return mediaInfo{}, fmt.Errorf("ffprobe json: %w", err)
	}
	info := mediaInfo{SizeBytes: parseInt64(parsed.Format.Size), Bitrate: int(parseInt64(parsed.Format.BitRate))}
	found := false
	for _, stream := range parsed.Streams {
		if stream.CodecType != "audio" {
			continue
		}
		found = true
		info.Codec = stream.CodecName
		info.Channels = stream.Channels
		info.SampleRate = int(parseInt64(stream.SampleRate))
		if stream.Duration != "" {
			info.DurationMS = milliseconds(stream.Duration)
		}
		break
	}
	if !found {
		return mediaInfo{}, fmt.Errorf("ffprobe: no audio stream")
	}
	if info.DurationMS == 0 {
		info.DurationMS = milliseconds(parsed.Format.Duration)
	}
	if info.DurationMS <= 0 {
		return mediaInfo{}, fmt.Errorf("ffprobe: duration is empty")
	}
	return info, nil
}

type ffmpegError struct {
	err    error
	detail string
}

func (e *ffmpegError) Error() string {
	if e.detail == "" {
		return "ffmpeg: " + e.err.Error()
	}
	return "ffmpeg: " + e.err.Error() + ": " + e.detail
}

func (e *ffmpegError) Unwrap() error {
	return e.err
}

func transcode(ctx context.Context, source, dest string) error {
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-y",
		"-i", source,
		"-vn",
		"-map", "0:a:0",
		"-c:a", "aac",
		"-b:a", "192k",
		"-ac", "2",
		"-movflags", "+faststart",
		dest,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		text := strings.TrimSpace(stderr.String())
		if len(text) > 400 {
			text = text[len(text)-400:]
		}
		return &ffmpegError{err: err, detail: text}
	}
	return nil
}

func milliseconds(raw string) int {
	seconds, err := strconv.ParseFloat(raw, 64)
	if err != nil || seconds <= 0 {
		return 0
	}
	return int(seconds*1000 + 0.5)
}

func parseInt64(raw string) int64 {
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0
	}
	return n
}
