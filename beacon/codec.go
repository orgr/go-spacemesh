package beacon

import "math/big"

const (
	up = uint(1)
)

func encodeVotes(currentRound allVotes, firstRound [][]byte) []byte {
	var bits big.Int
	for i, v := range firstRound {
		if _, ok := currentRound.valid[string(v)]; ok {
			bits.SetBit(&bits, i, up)
		}
		// no need to set invalid votes as big.Int will have unset bits
		// return the default value 0
	}
	return bits.Bytes()
}

func decodeVotes(votesBitVector []byte, firstRound [][]byte) allVotes {
	result := allVotes{
		valid:   make(proposalSet),
		invalid: make(proposalSet),
	}
	bits := new(big.Int).SetBytes(votesBitVector)
	for i, proposal := range firstRound {
		if bits.Bit(i) == up {
			result.valid[string(proposal)] = struct{}{}
		} else {
			result.invalid[string(proposal)] = struct{}{}
		}
	}
	return result
}
