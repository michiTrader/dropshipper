package sales

import "testing"

func TestEmailProblem(t *testing.T) {
	cases := []struct {
		in, sug string
		bad     bool
	}{
		{"samuel@gmail.com", "", false},
		{"Samuel@Gmail.com", "", false},
		{"samuelarrobagmail.com", "samuel@gmail.com", true},
		{"samuel arroba gmail.com", "samuel@gmail.com", true},
		{"accionesgaviriaarrobagmail.com", "accionesgaviria@gmail.com", true},
		{"accionesgaviriaarrob@gmail.com", "accionesgaviria@gmail.com", true},
		{"samuelgmail.com", "samuel@gmail.com", true},
		{"samuel@gmial.com", "samuel@gmail.com", true},
		{"samuel@gmail.con", "samuel@gmail.com", true},
		{"samuel@hotmial.com", "samuel@hotmail.com", true},
		{"samuel", "", true},
		{"@gmail.com", "", true},
		{"hola que tal", "", true},
	}
	for _, c := range cases {
		sug, bad := EmailProblem(c.in)
		if bad != c.bad || sug != c.sug {
			t.Errorf("EmailProblem(%q) = (%q,%v), quiero (%q,%v)", c.in, sug, bad, c.sug, c.bad)
		}
	}
}
