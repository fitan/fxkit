package di

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"runtime"
	"strings"
	"time"

	"github.com/samber/do/v2"
	typetostring "github.com/samber/go-type-to-string"
)

// Injector 是依赖注入容器接口，直接复用 samber/do/v2.Injector。
type Injector = do.Injector

// Provider 是类型安全的构造函数。
type Provider[T any] = do.Provider[T]

// Option 是模块或配置的装配函数。
type Option func(Injector)

// Module 将一组 Option 命名组合为一个 Option。
func Module(name string, opts ...Option) Option {
	return func(i Injector) {
		for _, opt := range opts {
			if opt != nil {
				opt(i)
			}
		}
	}
}

// Options 将多个 Option 组合成单个 Option。
func Options(opts ...Option) Option {
	return func(i Injector) {
		for _, opt := range opts {
			if opt != nil {
				opt(i)
			}
		}
	}
}

// Provide 向容器中注册一个 Provider。
func Provide[T any](i Injector, provider Provider[T]) {
	do.Provide(i, provider)
}

// ProvideValue 向容器中注册一个已有的值。
func ProvideValue[T any](i Injector, value T) {
	do.ProvideValue(i, value)
}

// ProvideNamed 向容器中注册一个带名称的 Provider。
func ProvideNamed[T any](i Injector, name string, provider Provider[T]) {
	do.ProvideNamed(i, name, provider)
}

// Invoke 从容器中解析类型 T。
//
// 解析顺序：
//  1. samber/do 原生按类型解析（适用于 do.Provide/ProvideValue/ProvideNamed）。
//  2. 回退到按「类型名」注册的 any 服务（[ToOption] 为无类型参数构造函数
//     建立的注册，例如 func(dep *Dep) (*Svc, error)）。
//
// 两条路径都失败时返回带原始错误的说明，而不是静默返回零值。
func Invoke[T any](i Injector) (T, error) {
	var zero T
	if i == nil {
		return zero, fmt.Errorf("di: nil injector resolving %T", zero)
	}

	// 原生路径：类型精确匹配（含接口别名），也保留 do 的循环依赖检测。
	if v, err := do.Invoke[T](i); err == nil {
		return v, nil
	}

	// 回退路径：反射注册的构造函数以 `any` 存放，只能按服务名取回。
	name := typetostring.GetType[T]()
	v, err := do.InvokeNamed[any](i, name)
	if err != nil {
		return zero, err
	}
	typed, ok := v.(T)
	if !ok {
		return zero, fmt.Errorf("di: service %s has type %T, want %T", name, v, zero)
	}
	return typed, nil
}

// MustInvoke 从容器中强制解析类型 T，失败时 panic。
func MustInvoke[T any](i Injector) T {
	v, err := Invoke[T](i)
	if err != nil {
		panic(err)
	}
	return v
}

// RootScope 包装根注入容器。
type RootScope = do.RootScope

// Shutdown 关闭容器并清理 group 注册表中该容器的记录。
//
// 直接调用 `root.Shutdown()` 也能正常关闭，只是会在 groupRegistry 里
// 留下一条空壳记录（持有 Group 指针，不持有整个容器）。测试或长驻进程
// 反复创建/销毁容器时用本函数可彻底回收。
func Shutdown(root *RootScope) *do.ShutdownReport {
	if root == nil {
		return nil
	}
	report := root.Shutdown()
	ReleaseInjector(root)
	return report
}

// New 创建一个全新的 RootScope，并注入内置核心服务（如 Lifecycle）。
func New(opts ...Option) *RootScope {
	root := do.New()
	// 标记装配区间：严格装配模式下，此时读 group 会 panic。
	done := markAssembling(root)
	defer done()

	lc := NewLifecycle()
	do.ProvideValue(root, lc)

	for _, opt := range opts {
		if opt != nil {
			opt(root)
		}
	}
	return root
}

// ToOption 将任意函数适配为 Option。
//
// 支持的形式：
//
//	Option / func(Injector)
//	非函数值            → ProvideValue（按值的动态类型注册）
//	func()              → 装配期立即执行（无返回值）
//	func(a, b, ...) T   → 按返回类型注册，调用时从容器解析每个参数
//	func(a, ...) (T, error)
//
// 无法解析参数类型或返回类型不受支持时 **panic**，而不是静默丢弃 ——
// 静默丢弃会让「模块看起来装配成功、运行期服务却不存在」，极难排查。
func ToOption(fn any) Option {
	if fn == nil {
		return nil
	}
	if opt, ok := fn.(Option); ok {
		return opt
	}
	if opt, ok := fn.(func(Injector)); ok {
		return opt
	}

	val := reflect.ValueOf(fn)
	if val.Kind() != reflect.Func {
		return func(i Injector) {
			provideDynamicValue(i, fn)
		}
	}

	typ := val.Type()
	if typ.NumOut() == 0 {
		return func(i Injector) {
			callForSideEffect(i, val)
		}
	}
	return func(i Injector) {
		provideReflect(i, val)
	}
}

// provideDynamicValue 按值的动态类型注册一个单例。
// 需要反射路径，因为泛型 ProvideValue[T] 的 T 在编译期已固定为 any。
func provideDynamicValue(i Injector, v any) {
	if v == nil {
		panic("di: cannot provide a nil value; use a typed nil pointer or ProvideValue")
	}
	name := typetostring.GetReflectType(reflect.TypeOf(v))
	if serviceExists(i, name) {
		slog.Warn("di: service already registered, skipping duplicate value", "service", name)
		return
	}
	do.ProvideNamed[any](i, name, func(do.Injector) (any, error) {
		return v, nil
	})
}

// callForSideEffect 执行 func(...) 形式的装配函数。
// 只支持参数全部可从容器解析的情况；无法解析时 panic（见 [ToOption]）。
func callForSideEffect(i Injector, val reflect.Value) {
	args, err := resolveArgs(i, val.Type())
	if err != nil {
		panic(fmt.Sprintf("di: cannot wire %s: %v", fnName(val), err))
	}
	val.Call(args)
}

// provideReflect 注册任意签名的构造函数：
// 从返回值类型推导服务名，从参数类型推导依赖名。
func provideReflect(i Injector, val reflect.Value) {
	typ := val.Type()
	if err := validateConstructor(typ); err != nil {
		panic(fmt.Sprintf("di: cannot wire %s: %v", fnName(val), err))
	}

	outType := typ.Out(0)
	name := typetostring.GetReflectType(outType)

	if serviceExists(i, name) {
		// samber/do 对同名注册会 panic；这里降级为告警以保持「同类服务可被多次提供」
		// 的历史行为（例如多模块都注册 *Config 时只有第一个生效）。
		slog.Warn("di: service already registered, skipping duplicate provider",
			"service", name, "provider", fnName(val))
		return
	}

	do.ProvideNamed[any](i, name, func(inj Injector) (any, error) {
		args, err := resolveArgs(inj, typ)
		if err != nil {
			return nil, fmt.Errorf("di: resolve args for %s: %w", name, err)
		}
		outs := val.Call(args)
		if n := len(outs); n == 2 {
			if e, ok := outs[1].Interface().(error); ok && e != nil {
				return nil, e
			}
		}
		return outs[0].Interface(), nil
	})
}

// resolveArgs 依据 fn 的入参类型从容器逐个解析。
//
// 支持两种参数形态：
//   - 普通依赖类型（指针/接口/值）：按类型名解析。
//   - **参数结构体**（fx 风格 params struct，非指针）：逐字段解析后构造，
//     字段标记 `optional:"true"` 时解析失败留零值。这让
//     func(p SomeParams) 这类构造函数能直接工作。
func resolveArgs(i Injector, fnType reflect.Type) ([]reflect.Value, error) {
	args := make([]reflect.Value, fnType.NumIn())
	for idx := 0; idx < fnType.NumIn(); idx++ {
		argType := fnType.In(idx)

		if isParamsStruct(argType) {
			v, err := buildParamsStruct(i, argType)
			if err != nil {
				return nil, err
			}
			args[idx] = v
			continue
		}

		rv, err := resolveOne(i, argType)
		if err != nil {
			return nil, err
		}
		args[idx] = rv
	}
	return args, nil
}

// resolveOne 解析单个依赖值。
func resolveOne(i Injector, argType reflect.Type) (reflect.Value, error) {
	depName := typetostring.GetReflectType(argType)
	v, err := do.InvokeNamed[any](i, depName)
	if err != nil {
		return reflect.Value{}, fmt.Errorf("dependency %s: %w", depName, err)
	}
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return reflect.Value{}, fmt.Errorf("dependency %s: resolved nil", depName)
	}
	if !rv.Type().AssignableTo(argType) {
		return reflect.Value{}, fmt.Errorf("dependency %s: got %s, want %s", depName, rv.Type(), argType)
	}
	return rv, nil
}

// isParamsStruct 判断是否为 fx 风格的参数结构体（值类型的 struct）。
func isParamsStruct(t reflect.Type) bool {
	return t.Kind() == reflect.Struct && t != reflect.TypeFor[time.Time]()
}

// buildParamsStruct 逐字段填充参数结构体。
//
// 字段 `Foo []T` 会从 `*di.Group[T]` 取值（fx 的 group 语义）。
func buildParamsStruct(i Injector, t reflect.Type) (reflect.Value, error) {
	out := reflect.New(t).Elem()
	for idx := 0; idx < t.NumField(); idx++ {
		sf := t.Field(idx)
		if !sf.IsExported() {
			continue
		}
		fv := out.Field(idx)

		// group 语义：[]T ← *di.Group[T]
		//
		// 只要容器里存在该元素类型的 group（哪怕当前为空），就填充它。
		// 否则 []T 会被当成普通依赖去容器里找 `[]T` 服务，报 "could not find service []string"。
		if sf.Type.Kind() == reflect.Slice {
			if groupExists(i, sf.Type.Elem()) {
				items, _ := groupItems(i, sf.Type.Elem())
				slice := reflect.MakeSlice(sf.Type, 0, len(items))
				for _, item := range items {
					rv := reflect.ValueOf(item)
					if !rv.IsValid() || !rv.Type().AssignableTo(sf.Type.Elem()) {
						continue
					}
					slice = reflect.Append(slice, rv)
				}
				fv.Set(slice)
				continue
			}
		}

		v, err := do.InvokeNamed[any](i, typetostring.GetReflectType(sf.Type))
		if err != nil {
			if isOptionalField(sf) {
				continue // 留零值（nil 指针 / fallback 语义由此产生）
			}
			return reflect.Value{}, fmt.Errorf("dependency %s.%s: %w", t.Name(), sf.Name, err)
		}
		if v == nil {
			// 显式注册成 nil 的服务（例如 auth 关闭时的 TokenValidator）。
			if isNilable(sf.Type) || isOptionalField(sf) {
				continue
			}
			return reflect.Value{}, fmt.Errorf("dependency %s.%s: resolved nil", t.Name(), sf.Name)
		}
		rv := reflect.ValueOf(v)
		if !rv.Type().AssignableTo(sf.Type) {
			if isOptionalField(sf) {
				continue
			}
			return reflect.Value{}, fmt.Errorf("dependency %s.%s: got %T, want %s", t.Name(), sf.Name, v, sf.Type)
		}
		fv.Set(rv)
	}
	return out, nil
}

// groupItems 把 []T 字段解析为容器 i 中对应 group 的条目。
func groupItems(i Injector, elem reflect.Type) ([]any, bool) {
	return groupValues(i, elem)
}

// groupExists 报告容器 i 中是否已登记过元素类型为 elem 的 group。
func groupExists(i Injector, elem reflect.Type) bool {
	groupRegistryMu.RLock()
	defer groupRegistryMu.RUnlock()
	_, ok := groupRegistry[groupKey{owner: ownerID(i), elem: elem}]
	return ok
}

// isNilable 报告类型能否表示 nil（指针、接口、map、slice、func、chan）。
func isNilable(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan:
		return true
	default:
		return false
	}
}

// isOptionalField 报告参数结构体字段是否声明了 optional:"true"。
func isOptionalField(sf reflect.StructField) bool {
	v, ok := sf.Tag.Lookup("optional")
	return ok && (v == "true" || v == "")
}

// CallConstructor 从容器解析 fn 的全部入参并调用它，返回第一个返回值。
// fn 必须是 func(dep...) T 或 func(dep...) (T, error)。
// 供需要「延迟到依赖就绪后再构造」的 group 贡献点使用（见 huma.ProvideRegistrar）。
func CallConstructor(i Injector, fn any) (any, error) {
	if fn == nil {
		return nil, fmt.Errorf("di: nil constructor")
	}
	val := reflect.ValueOf(fn)
	if val.Kind() != reflect.Func {
		return nil, fmt.Errorf("di: %T is not a func", fn)
	}
	typ := val.Type()
	if err := ValidateConstructor(typ); err != nil {
		return nil, fmt.Errorf("di: %s: %w", fnName(val), err)
	}

	args, err := resolveArgs(i, typ)
	if err != nil {
		return nil, fmt.Errorf("di: %s: %w", fnName(val), err)
	}
	outs := val.Call(args)
	if len(outs) == 2 {
		if e, ok := outs[1].Interface().(error); ok && e != nil {
			return nil, e
		}
	}
	return outs[0].Interface(), nil
}

// ValidateConstructor 校验构造函数签名是否为 (T) 或 (T, error)。
func ValidateConstructor(typ reflect.Type) error {
	return validateConstructor(typ)
}

// ToInvokeOption 把一个函数注册为「启动时执行的副作用」，而不是服务。
//
//	fn 的入参按依赖解析；返回值可以是 () 或 (error)。
//	执行时机为 Lifecycle.OnStart，返回的 error 会中止启动。
//
// 签名不合法或容器中缺少 *Lifecycle 时立即 panic —— 装配期发现问题，
// 比等到启动时静默失败要好。
func ToInvokeOption(fn any) Option {
	if fn == nil {
		return nil
	}
	val := reflect.ValueOf(fn)
	if val.Kind() != reflect.Func {
		panic("di.ToInvokeOption: " + reflect.TypeOf(fn).String() + " is not a func")
	}
	typ := val.Type()
	if typ.NumOut() > 1 || (typ.NumOut() == 1 && !typ.Out(0).Implements(reflect.TypeFor[error]())) {
		panic("di.ToInvokeOption: " + fnName(val) + " must return () or (error)")
	}

	return func(i Injector) {
		lc, err := Invoke[*Lifecycle](i)
		if err != nil || lc == nil {
			panic("di.ToInvokeOption: *di.Lifecycle missing from the graph")
		}
		lc.Append(Hook{
			OnStart: func(context.Context) error {
				args, argErr := resolveArgs(i, typ)
				if argErr != nil {
					return fmt.Errorf("di: invoke %s: %w", fnName(val), argErr)
				}
				outs := val.Call(args)
				if len(outs) == 1 {
					if e, ok := outs[0].Interface().(error); ok && e != nil {
						return e
					}
				}
				return nil
			},
		})
	}
}

// validateConstructor 检查构造函数签名是否可装配。
func validateConstructor(typ reflect.Type) error {
	switch typ.NumOut() {
	case 1:
	case 2:
		if !typ.Out(1).Implements(reflect.TypeFor[error]()) {
			return fmt.Errorf("second return value must be error, got %s", typ.Out(1))
		}
	default:
		return fmt.Errorf("constructor must return (T) or (T, error), got %d returns", typ.NumOut())
	}
	for idx := 0; idx < typ.NumIn(); idx++ {
		if typ.IsVariadic() && idx == typ.NumIn()-1 {
			return fmt.Errorf("variadic parameter is not supported")
		}
	}
	return nil
}

// serviceExists 判断服务名是否已注册。
func serviceExists(i Injector, name string) bool {
	for _, s := range i.ListProvidedServices() {
		if s.Service == name {
			return true
		}
	}
	return false
}

// fnName 返回便于排查的函数名。
func fnName(val reflect.Value) string {
	if val.Kind() != reflect.Func || val.IsNil() {
		return "<nil func>"
	}
	fn := val.Pointer()
	if f := runtime.FuncForPC(fn); f != nil {
		return splitPackageFunc(f.Name())
	}
	return fmt.Sprintf("func@%#x", fn)
}

// splitPackageFunc 把 "pkg/path.Func" 拆成便于日志展示的形式。
func splitPackageFunc(full string) string {
	if idx := strings.LastIndex(full, "/"); idx >= 0 {
		return full[idx+1:]
	}
	return full
}
