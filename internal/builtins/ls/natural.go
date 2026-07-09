package ls

import "strings"

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// digitRun returns the maximal run of digits in s starting at i, and the index
// just past it.
func digitRun(s string, i int) (run string, next int) {
	j := i
	for j < len(s) && isDigit(s[j]) {
		j++
	}
	return s[i:j], j
}

// naturalLess reports whether a sorts before b using a case-insensitive,
// numeric-aware ("natural") ordering, so "file2" precedes "file10". A
// case-sensitive comparison breaks ties for deterministic results.
func naturalLess(a, b string) bool {
	la, lb := strings.ToLower(a), strings.ToLower(b)
	ia, ib := 0, 0

	for ia < len(la) && ib < len(lb) {
		ca, cb := la[ia], lb[ib]
		if isDigit(ca) && isDigit(cb) {
			ra, na := digitRun(la, ia)
			rb, nb := digitRun(lb, ib)
			va := strings.TrimLeft(ra, "0")
			vb := strings.TrimLeft(rb, "0")
			switch {
			case len(va) != len(vb):
				return len(va) < len(vb) // more significant digits = larger
			case va != vb:
				return va < vb
			case len(ra) != len(rb):
				return len(ra) < len(rb) // equal value, fewer leading zeros first
			}
			ia, ib = na, nb
			continue
		}
		if ca != cb {
			return ca < cb
		}
		ia++
		ib++
	}

	if (len(la) - ia) != (len(lb) - ib) {
		return (len(la) - ia) < (len(lb) - ib)
	}
	return a < b
}
