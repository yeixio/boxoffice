package par2

// Arithmetic in GF(2^16), the field par2's Reed-Solomon code works in, with
// the generator polynomial x^16 + x^12 + x^3 + x + 1 (0x1100B) par2 uses.

const (
	gfBits  = 16
	gfSize  = 1 << gfBits
	gfLimit = gfSize - 1
	gfPoly  = 0x1100B
)

var (
	gfLog [gfSize]uint16
	gfExp [gfSize * 2]uint16
)

func init() {
	b := 1
	for l := range gfLimit {
		gfExp[l] = uint16(b)
		gfLog[b] = uint16(l)
		b <<= 1
		if b&gfSize != 0 {
			b ^= gfPoly
		}
	}
	for l := gfLimit; l < len(gfExp); l++ {
		gfExp[l] = gfExp[l-gfLimit]
	}
}

func gfMul(a, b uint16) uint16 {
	if a == 0 || b == 0 {
		return 0
	}
	return gfExp[int(gfLog[a])+int(gfLog[b])]
}

func gfDiv(a, b uint16) uint16 {
	if a == 0 {
		return 0
	}
	if b == 0 {
		panic("par2: divide by zero")
	}
	return gfExp[int(gfLog[a])+gfLimit-int(gfLog[b])]
}

// gfPow is a^n.
func gfPow(a uint16, n int) uint16 {
	if n == 0 {
		return 1
	}
	if a == 0 {
		return 0
	}
	return gfExp[(int(gfLog[a])*n)%gfLimit]
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// inputConstants are the constants par2 gives the first n input slices:
// 2^k for the k (from 1 up) that share no factor with 65535.
func inputConstants(n int) []uint16 {
	out := make([]uint16, 0, n)
	for k := 0; len(out) < n && k < gfLimit; k++ {
		if gcd(gfLimit, k) != 1 {
			continue
		}
		out = append(out, gfExp[k])
	}
	return out
}

// mulTable is c times every 16-bit word, split by byte so a word takes two
// lookups.
type mulTable struct{ lo, hi [256]uint16 }

func newMulTable(c uint16) *mulTable {
	t := &mulTable{}
	for i := range 256 {
		t.lo[i] = gfMul(c, uint16(i))
		t.hi[i] = gfMul(c, uint16(i)<<8)
	}
	return t
}

// mulAdd adds c*src to dst, word by word (little-endian 16-bit words).
func (t *mulTable) mulAdd(dst, src []byte) {
	n := min(len(dst), len(src)) &^ 1
	dst, src = dst[:n], src[:n]
	for i := 0; i+1 < n; i += 2 {
		v := t.lo[src[i]] ^ t.hi[src[i+1]]
		dst[i] ^= byte(v)
		dst[i+1] ^= byte(v >> 8)
	}
}

// invert inverts a square matrix in place by Gauss-Jordan elimination.
func invert(m [][]uint16) bool {
	n := len(m)
	inv := make([][]uint16, n)
	for i := range inv {
		inv[i] = make([]uint16, n)
		inv[i][i] = 1
	}
	for col := range n {
		p := -1
		for r := col; r < n; r++ {
			if m[r][col] != 0 {
				p = r
				break
			}
		}
		if p < 0 {
			return false
		}
		m[col], m[p] = m[p], m[col]
		inv[col], inv[p] = inv[p], inv[col]
		if d := m[col][col]; d != 1 {
			for j := range n {
				m[col][j] = gfDiv(m[col][j], d)
				inv[col][j] = gfDiv(inv[col][j], d)
			}
		}
		for r := range n {
			if r == col || m[r][col] == 0 {
				continue
			}
			f := m[r][col]
			for j := range n {
				m[r][j] ^= gfMul(f, m[col][j])
				inv[r][j] ^= gfMul(f, inv[col][j])
			}
		}
	}
	copy(m, inv)
	return true
}
