package audio

import "testing"

func TestResamplerDownsamples24KTo16K20MS(t *testing.T) {
	input := Int16ToBytes(rampSamples(480))
	resampler := NewResampler(24000, 16000)

	output := resampler.Process(input)

	if len(output) != 320 {
		t.Fatalf("output samples = %d, want 320", len(output))
	}
	if got := len(Int16ToBytes(output)); got != 640 {
		t.Fatalf("output bytes = %d, want 640", got)
	}
	if output[0] != 0 {
		t.Fatalf("first sample = %d, want 0", output[0])
	}
	if output[1] != 1 {
		t.Fatalf("second sample = %d, want interpolated 1", output[1])
	}
	if output[2] != 3 {
		t.Fatalf("third sample = %d, want 3", output[2])
	}
}

func TestResamplerStreamingMatchesSingleChunk(t *testing.T) {
	input := Int16ToBytes(rampSamples(960))

	single := NewResampler(24000, 16000).ProcessBytes(input)

	streamingResampler := NewResampler(24000, 16000)
	var streaming []byte
	for _, chunk := range [][]byte{
		input[:137],
		input[137:511],
		input[511:1001],
		input[1001:],
	} {
		streaming = append(streaming, streamingResampler.ProcessBytes(chunk)...)
	}

	if len(streaming) != len(single) {
		t.Fatalf("streaming bytes = %d, want %d", len(streaming), len(single))
	}
	for i := range single {
		if streaming[i] != single[i] {
			t.Fatalf("streaming[%d] = %d, want %d", i, streaming[i], single[i])
		}
	}
}

func TestResamplerPreservesOddByteAcrossChunks(t *testing.T) {
	input := Int16ToBytes([]int16{100, 200, 300, 400})
	resampler := NewResampler(16000, 16000)

	first := resampler.Process(input[:3])
	second := resampler.Process(input[3:])

	got := append(first, second...)
	want := []int16{100, 200, 300, 400}
	if len(got) != len(want) {
		t.Fatalf("samples = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sample[%d] = %d, want %d", i, got[i], want[i])
		}
	}
}

func rampSamples(count int) []int16 {
	samples := make([]int16, count)
	for i := range samples {
		samples[i] = int16(i)
	}
	return samples
}
