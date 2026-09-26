package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain/model"
)

// TestMechanismPackagesStayDomainAgnostic 守护 D64：变更、执行与运行内核只做机制，创作规则
// 在 model 的函数里。禁止集合从 model 源码推导——文档、任务、蓝图、事实、实体与目标种类
// 常量（名字与字面值都不得出现）以及具体任务输入类型，新增种类自动纳入；change 只认识
// 内核控制对象 Ownership、Approval、Directive（§4.7、§5.4、D33/D48）。
func TestMechanismPackagesStayDomainAgnostic(t *testing.T) {
	kinds, inputs := domainSymbols(t)
	forbidden := make(map[string]bool)
	for name := range kinds {
		forbidden[name] = true
	}
	for _, name := range append(inputs,
		"NovelGoal", "DecodeNovelGoal", "Intent", "PlanNode", "CanonFact", "ManuscriptChapter", "Compass",
		"Entity", "ReviewVerdict", "ReviewFinding", "Adjudication", "SemanticImpactReport", "ResolutionStrategy",
	) {
		forbidden[name] = true
	}
	allowed := map[string][]string{"change": {"DocumentOwnership", "DocumentApproval", "DocumentDirective"}}
	for _, pkg := range []string{"change", "operation", "creation"} {
		literals := make(map[string]string) // 种类字面值 → 常量名：绕过常量写字符串同样禁止
		for name, value := range kinds {
			if !slices.Contains(allowed[pkg], name) {
				literals[value] = name
			}
		}
		err := filepath.WalkDir(filepath.Join("..", "domain", pkg), func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return walkErr
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				return err
			}
			domainAlias := "model"
			for _, spec := range file.Imports {
				if strings.Trim(spec.Path.Value, `"`) == modulePath+"/internal/domain/model" && spec.Name != nil {
					domainAlias = spec.Name.Name
					if domainAlias == "." {
						t.Errorf("%s 不得点导入 domain，业务符号必须可审查", path)
					}
				}
			}
			ast.Inspect(file, func(node ast.Node) bool {
				switch node := node.(type) {
				case *ast.SelectorExpr:
					pkgIdent, ok := node.X.(*ast.Ident)
					name := node.Sel.Name
					if ok && pkgIdent.Name == domainAlias && forbidden[name] && !slices.Contains(allowed[pkg], name) {
						t.Errorf("%s 引用了 model.%s：内核只做机制，创作规则放进 model 的函数（D64）", path, name)
					}
				case *ast.BasicLit:
					if node.Kind != token.STRING {
						return true
					}
					if name, banned := literals[strings.Trim(node.Value, "`\"")]; banned {
						t.Errorf("%s 写了种类字面值 %s（即 model.%s）：内核只做机制（D64）", path, node.Value, name)
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// domainSymbols 从 model 源码收集种类常量（名称 → 值）与具体任务输入类型，并核对种类覆盖
// 运行时登记的全部文档种类与任务种类，推导失效时守护本身报错而不是静默放行。
func domainSymbols(t *testing.T) (map[string]string, []string) {
	t.Helper()
	kindTypes := []string{"DocumentKind", "OperationKind", "PlanNodeKind", "CanonFactKind", "EntityKind", "GoalKind"}
	root := filepath.Join("..", "domain", "model")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	constants := make(map[string]string)
	var inputs []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, entry.Name()), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if ok && gen.Tok == token.TYPE {
				for _, spec := range gen.Specs {
					typeSpec := spec.(*ast.TypeSpec)
					if _, isStruct := typeSpec.Type.(*ast.StructType); isStruct && strings.HasSuffix(typeSpec.Name.Name, "Input") {
						inputs = append(inputs, typeSpec.Name.Name)
					}
				}
			}
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				value := spec.(*ast.ValueSpec)
				typ, ok := value.Type.(*ast.Ident)
				if !ok || !slices.Contains(kindTypes, typ.Name) {
					continue
				}
				for i, name := range value.Names {
					if literal, ok := value.Values[i].(*ast.BasicLit); ok {
						constants[name.Name] = strings.Trim(literal.Value, `"`)
					}
				}
			}
		}
	}
	values := make(map[string]bool, len(constants))
	for _, value := range constants {
		values[value] = true
	}
	for _, authority := range []model.AuthorityKind{model.AuthorityProject, model.AuthorityProfile, model.AuthorityPack} {
		for _, kind := range model.DocumentKindsFor(authority) {
			if !values[string(kind)] {
				t.Fatalf("禁止集合漏了文档种类 %q：种类常量必须以 DocumentKind 类型声明", kind)
			}
		}
	}
	for _, spec := range model.OperationKinds() {
		if !values[string(spec.Kind)] {
			t.Fatalf("禁止集合漏了任务种类 %q：种类常量必须以 OperationKind 类型声明", spec.Kind)
		}
	}
	if len(inputs) == 0 {
		t.Fatal("没有推导出任何任务输入类型")
	}
	return constants, inputs
}

// The novel policy can inspect snapshots and return a step; I/O belongs to Goal.
func TestNovelDeriverStaysPure(t *testing.T) {
	path := filepath.Join("..", "app", "novel", "novel_goal.go")
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	for _, spec := range file.Imports {
		imported := strings.Trim(spec.Path.Value, `"`)
		dependency, internal := strings.CutPrefix(imported, modulePath+"/internal/")
		if !internal {
			continue
		}
		switch dependency {
		case "domain/model", "domain/narrative", "app/project", "domain/creation": // narrative 只由快照渲染故事语言，同样是纯函数
		default:
			t.Errorf("%s 依赖了 %s：纯规则只读快照并返回步骤，不能加载状态或执行任务（D49）", path, imported)
		}
	}
}
