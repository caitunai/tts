package audio

import "encoding/binary"

// Resampler converts a streaming s16le mono PCM stream between sample rates.
// It keeps a small tail from the previous chunk so interpolation stays
// continuous across arbitrary provider read boundaries.
type Resampler struct {
	remainder     []int16
	byteRemainder []byte
	inRate        int
	outRate       int
	ratio         float64
	pos           float64
}

// NewResampler creates a linear-interpolation resampler.
func NewResampler(inRate, outRate int) *Resampler {
	return &Resampler{
		inRate:    inRate,
		outRate:   outRate,
		ratio:     float64(inRate) / float64(outRate),
		remainder: make([]int16, 0, 10),
		pos:       0,
	}
}

// BytesToInt16 converts little-endian bytes to int16 samples. A trailing odd
// byte is ignored; Process preserves that byte across chunks.
func (r *Resampler) BytesToInt16(data []byte) []int16 {
	if len(data)%2 != 0 {
		data = data[:len(data)-1]
	}

	numSamples := len(data) / 2
	samples := make([]int16, numSamples)

	for i := 0; i < numSamples; i++ {
		samples[i] = int16(binary.LittleEndian.Uint16(data[i*2 : i*2+2]))
	}

	return samples
}

// ProcessBytes handles one streaming PCM byte chunk and returns resampled PCM
// bytes in s16le format.
func (r *Resampler) ProcessBytes(inputBytes []byte) []byte {
	return Int16ToBytes(r.Process(inputBytes))
}

// Process handles one streaming PCM byte chunk and returns resampled samples.
func (r *Resampler) Process(inputBytes []byte) []int16 {
	if len(r.byteRemainder) > 0 {
		combined := make([]byte, 0, len(r.byteRemainder)+len(inputBytes))
		combined = append(combined, r.byteRemainder...)
		combined = append(combined, inputBytes...)
		inputBytes = combined
		r.byteRemainder = nil
	}
	if len(inputBytes)%2 != 0 {
		r.byteRemainder = append(r.byteRemainder[:0], inputBytes[len(inputBytes)-1])
		inputBytes = inputBytes[:len(inputBytes)-1]
	}
	if len(inputBytes) == 0 {
		return nil
	}

	newSamples := r.BytesToInt16(inputBytes)
	if r.inRate == r.outRate {
		output := make([]int16, len(newSamples))
		copy(output, newSamples)
		return output
	}
	if r.inRate <= 0 || r.outRate <= 0 {
		return nil
	}

	fullInput := make([]int16, len(r.remainder)+len(newSamples))
	copy(fullInput, r.remainder)
	copy(fullInput[len(r.remainder):], newSamples)

	inputLen := len(fullInput)
	if inputLen < 2 {
		r.remainder = fullInput
		return []int16{}
	}

	estimatedOutLen := int(float64(inputLen) / r.ratio)
	output := make([]int16, 0, estimatedOutLen)

	for int(r.pos+1) < inputLen {
		index := int(r.pos)
		frac := r.pos - float64(index)

		s0 := float64(fullInput[index])
		s1 := float64(fullInput[index+1])

		val := s0 + (s1-s0)*frac
		if val > 32767 {
			val = 32767
		} else if val < -32768 {
			val = -32768
		}

		output = append(output, int16(val))
		r.pos += r.ratio
	}

	processedIntIndex := int(r.pos)
	r.pos -= float64(processedIntIndex)
	r.remainder = fullInput[processedIntIndex:]

	return output
}

// Int16ToBytes converts int16 samples to little-endian bytes.
func Int16ToBytes(samples []int16) []byte {
	if len(samples) == 0 {
		return nil
	}

	data := make([]byte, len(samples)*2)
	for i, sample := range samples {
		binary.LittleEndian.PutUint16(data[i*2:i*2+2], uint16(sample))
	}
	return data
}
