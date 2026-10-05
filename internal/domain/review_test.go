package domain

import (
	"reflect"
	"testing"
)

func TestRestoreOwnPlants(t *testing.T) {
	cases := []struct {
		name string
		prev []ForeshadowUpdate
		next []ForeshadowUpdate
		want []ForeshadowUpdate
	}{
		{
			name: "重写把本章 plant 写成 advance：plant 补回队首",
			prev: []ForeshadowUpdate{{ID: "f_photo", Action: "plant", Description: "泄洪道旧照"}},
			next: []ForeshadowUpdate{{ID: "f_photo", Action: "advance"}},
			want: []ForeshadowUpdate{
				{ID: "f_photo", Action: "plant", Description: "泄洪道旧照"},
				{ID: "f_photo", Action: "advance"},
			},
		},
		{
			name: "重写把本章 plant 整条丢了：仍补回",
			prev: []ForeshadowUpdate{{ID: "f_photo", Action: "plant", Description: "泄洪道旧照"}},
			next: nil,
			want: []ForeshadowUpdate{{ID: "f_photo", Action: "plant", Description: "泄洪道旧照"}},
		},
		{
			name: "新记录已自行声明 plant：不重复补",
			prev: []ForeshadowUpdate{{ID: "f_photo", Action: "plant", Description: "旧描述"}},
			next: []ForeshadowUpdate{{ID: "f_photo", Action: "plant", Description: "新描述"}},
			want: []ForeshadowUpdate{{ID: "f_photo", Action: "plant", Description: "新描述"}},
		},
		{
			name: "旧记录只有 advance/resolve：无 plant 可补",
			prev: []ForeshadowUpdate{{ID: "f_photo", Action: "advance"}, {ID: "f_key", Action: "resolve"}},
			next: []ForeshadowUpdate{{ID: "f_photo", Action: "resolve"}},
			want: []ForeshadowUpdate{{ID: "f_photo", Action: "resolve"}},
		},
		{
			name: "多条 plant 按原序补回，且都排在 advance 之前",
			prev: []ForeshadowUpdate{
				{ID: "f_a", Action: "plant", Description: "甲"},
				{ID: "f_b", Action: "plant", Description: "乙"},
			},
			next: []ForeshadowUpdate{{ID: "f_a", Action: "advance"}},
			want: []ForeshadowUpdate{
				{ID: "f_a", Action: "plant", Description: "甲"},
				{ID: "f_b", Action: "plant", Description: "乙"},
				{ID: "f_a", Action: "advance"},
			},
		},
		{
			name: "首次提交无旧记录：原样返回",
			prev: nil,
			next: []ForeshadowUpdate{{ID: "f_photo", Action: "plant", Description: "泄洪道旧照"}},
			want: []ForeshadowUpdate{{ID: "f_photo", Action: "plant", Description: "泄洪道旧照"}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RestoreOwnPlants(tc.prev, tc.next)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("RestoreOwnPlants() = %+v, want %+v", got, tc.want)
			}
		})
	}
}
