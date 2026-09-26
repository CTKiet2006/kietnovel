// Package assets 只负责把官方内置内容在编译期打包进二进制（§9.1），不含逻辑。
package assets

import (
	"embed"
	"io/fs"
)

//go:embed packs/official
var packs embed.FS

// OfficialPack 是官方内置 Novel Pack 的根目录（D65），按普通 Pack 加载。
func OfficialPack() fs.FS {
	root, err := fs.Sub(packs, "packs/official")
	if err != nil {
		panic(err) // 路径是编译期常量，失败只可能是代码写错
	}
	return root
}
