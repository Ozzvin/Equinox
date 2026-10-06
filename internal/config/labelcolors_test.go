package config

import "testing"

func TestAssignLabelColors(t *testing.T) {
	// one label has a colour; the next ones take the colours nobody has, in the palette order
	got := AssignLabelColors(map[string]string{"Кино": "pink"}, []string{"FatSeed", "Сериалы", "Игры"})
	want := map[string]string{"Кино": "pink", "FatSeed": "cyan", "Сериалы": "violet", "Игры": "olive"}
	for l, id := range want {
		if got[l] != id {
			t.Fatalf("%s: %q, want %q (%v)", l, got[l], id, got)
		}
	}
	// when the palette is used up, the colours repeat evenly
	var many []string
	for i := 0; i < 2*len(LabelPalette); i++ {
		many = append(many, string(rune('a'+i)))
	}
	used := map[string]int{}
	for _, id := range AssignLabelColors(nil, many) {
		used[id]++
	}
	for _, id := range LabelPalette {
		if used[id] != 2 {
			t.Fatalf("%s used %d times: %v", id, used[id], used)
		}
	}
}
