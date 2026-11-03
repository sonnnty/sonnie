package usage

import (
	"fmt"
	"math"
	"strconv"
	"time"
	"unicode/utf8"
)

// Gemini 2.5 Flash TTS list prices, no free tier. Source: Google Cloud
// Text-to-Speech pricing page (https://cloud.google.com/text-to-speech/pricing),
// "Gemini 2.5 Flash TTS", read on 2026-11-04. Update here if Google changes
// them; nothing else hard-codes a price.
const (
	// usdPerMillionInputTokens is the price of text input tokens.
	usdPerMillionInputTokens = 0.50
	// usdPerMillionAudioTokens is the price of audio output tokens.
	usdPerMillionAudioTokens = 10.00
	// audioTokensPerSecond is how Google converts generated audio to tokens.
	audioTokensPerSecond = 25

	// minOpusBitrate is the bits per second used to bound the duration of
	// audio whose length cannot be read (see audioDuration).
	minOpusBitrate = 6000
)

// EstimateInputTokens estimates the billed input tokens for one request as the
// number of runes in the text plus the style prompt. The real tokenizer is not
// available offline and not exposed by the REST response. A rune count
// over-counts for Thai (several characters per token) and is close for
// English, so the budget errs on the safe side. The input side is tiny next
// to the audio side (about 1/20 of the cost for 1 token per character), so
// the estimate error is immaterial.
func EstimateInputTokens(text, prompt string) int64 {
	return int64(utf8.RuneCountInString(text) + utf8.RuneCountInString(prompt))
}

// CostMicroTHB prices one successful Gemini-TTS request in millionths of a
// baht: audio seconds × 25 tokens × $10/1M plus input tokens × $0.50/1M,
// converted at usdToTHB. A micro-dollar times the rate is a micro-baht, so
// the arithmetic needs no further scaling.
func CostMicroTHB(audio time.Duration, inputTokens int64, usdToTHB float64) int64 {
	audioTokens := audio.Seconds() * audioTokensPerSecond
	microUSD := audioTokens*usdPerMillionAudioTokens + float64(inputTokens)*usdPerMillionInputTokens
	return int64(math.Round(microUSD * usdToTHB))
}

// MicroTHB converts a baht amount to micro-baht.
func MicroTHB(thb float64) int64 { return int64(math.Round(thb * 1e6)) }

// FormatTHB renders micro-baht as e.g. "฿240.00" or "฿1,240.50".
func FormatTHB(micro int64) string {
	satang := (micro + 5000) / 10000 // round to the nearest satang
	return fmt.Sprintf("฿%s.%02d", formatInt(satang/100), satang%100)
}

// FormatSeconds renders milliseconds of audio as whole seconds with one
// decimal, e.g. "12.3".
func FormatSeconds(ms int64) string { return strconv.FormatFloat(float64(ms)/1000, 'f', 1, 64) }
