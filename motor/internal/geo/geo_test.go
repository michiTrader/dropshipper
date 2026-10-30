package geo

import "testing"

func TestFind(t *testing.T) {
	if Count() < 1100 {
		t.Fatalf("tabla DANE incompleta: %d", Count())
	}
	cases := []struct{ dept, city, code string }{
		{"Antioquia", "Medellín", "05001"},
		{"antioquia", "medellin", "05001"},
		{"", "Bogotá", "11001"},
		{"Bogotá D.C.", "Bogotá, D.C.", "11001"},
		{"Cundinamarca", "Bogotá", "11001"},
		{"Valle del Cauca", "Cali", "76001"},
		{"Atlántico", "Barranquilla", "08001"},
		{"Bolívar", "Cartagena de Indias", "13001"},
	}
	for _, c := range cases {
		m, ok := Find(c.dept, c.city)
		if !ok || m.Code != c.code {
			t.Errorf("Find(%q,%q) = %+v %v, want %s", c.dept, c.city, m, ok, c.code)
		}
	}
	if m, _ := Find("Antioquia", "Medellín"); m.DANE7() != "0500100" {
		t.Errorf("DANE7 = %s", m.DANE7())
	}
	if _, ok := Find("Antioquia", "Narnia"); ok {
		t.Error("ciudad inexistente no debe resolverse")
	}
	// A homonym must respect the department.
	if m, ok := Find("Cundinamarca", "La Unión"); ok {
		t.Errorf("La Unión no está en Cundinamarca: %+v", m)
	}
	if m, ok := ByCode("0500100"); !ok || m.Name != "Medellín" {
		t.Errorf("ByCode: %+v %v", m, ok)
	}
}
