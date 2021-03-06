package compiler

import (
	"fmt"
	"math"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	golua "github.com/glycerine/golua/lua"
	"github.com/glycerine/luar"
)

type VmConfig struct {
	PreludePath string
	Quiet       bool
	NotTestMode bool // set to true for production, not running under test.
}

func NewVmConfig() *VmConfig {
	return &VmConfig{}
}

func NewLuaVmWithPrelude(cfg *VmConfig) (*golua.State, error) {
	vm := luar.Init() // does vm.OpenLibs() for us, adds luar. functions.

	if cfg == nil {
		cfg = NewVmConfig()
		cfg.PreludePath = "."
	}

	// load prelude
	files, err := FetchPreludeFilenames(cfg.PreludePath, cfg.Quiet)
	if err != nil {
		return nil, err
	}
	err = LuaDoFiles(vm, files)

	// load the utf8 library as __utf8
	cwd, err := os.Getwd()
	panicOn(err)
	panicOn(os.Chdir(cfg.PreludePath))
	LuaRunAndReport(vm, fmt.Sprintf(`__utf8 = require 'utf8'`))
	panicOn(os.Chdir(cwd))

	// take a Lua value, turn it into a Go value, wrap
	// it in a proxy and return it to Lua.
	lua2GoProxy := func(b interface{}) (a interface{}) {
		return b
	}

	luar.Register(vm, "", luar.Map{
		"__lua2go": lua2GoProxy,
	})
	//fmt.Printf("registered __lua2go with luar.\n")

	return vm, err
}

func LuaDoFiles(vm *golua.State, files []string) error {
	for _, f := range files {
		pp("LuaDoFiles, f = '%s'", f)
		if f == "lua.help.lua" {
			panic("where lua.help.lua?")
		}
		interr := vm.LoadString(fmt.Sprintf(`dofile("%s")`, f))
		if interr != 0 {
			pp("interr %v on vm.LoadString for dofile on '%s'", interr, f)
			msg := DumpLuaStackAsString(vm)
			vm.Pop(1)
			return fmt.Errorf("error in setupPrelude during LoadString on file '%s': Details: '%s'", f, msg)
		}
		err := vm.Call(0, 0)
		if err != nil {
			msg := DumpLuaStackAsString(vm)
			vm.Pop(1)
			return fmt.Errorf("error in setupPrelude during Call on file '%s': '%v'. Details: '%s'", f, err, msg)
		}
	}
	return nil
}

func DumpLuaStack(L *golua.State) {
	fmt.Printf("\n%s\n", DumpLuaStackAsString(L))
}

func DumpLuaStackAsString(L *golua.State) (s string) {
	var top int

	top = L.GetTop()
	s += fmt.Sprintf("========== begin DumpLuaStack: top = %v\n", top)
	for i := top; i >= 1; i-- {

		t := L.Type(i)
		s += fmt.Sprintf("DumpLuaStack: i=%v, t= %v\n", i, t)
		s += LuaStackPosToString(L, i)

	}
	s += fmt.Sprintf("========= end of DumpLuaStack\n")
	return
}

func LuaStackPosToString(L *golua.State, i int) string {
	t := L.Type(i)

	switch t {
	case golua.LUA_TNONE: // -1
		return fmt.Sprintf("LUA_TNONE; i=%v was invalid index", i)
	case golua.LUA_TNIL:
		return fmt.Sprintf("LUA_TNIL: nil")
	case golua.LUA_TSTRING:
		return fmt.Sprintf(" String : \t%v\n", L.ToString(i))
	case golua.LUA_TBOOLEAN:
		return fmt.Sprintf(" Bool : \t\t%v\n", L.ToBoolean(i))
	case golua.LUA_TNUMBER:
		return fmt.Sprintf(" Number : \t%v\n", L.ToNumber(i))
	case golua.LUA_TTABLE:
		return fmt.Sprintf(" Table : \n%s\n", dumpTableString(L, i))

	case 10: // LUA_TCDATA aka cdata
		//pp("Dump cdata case, L.Type(idx) = '%v'", L.Type(i))
		ctype := L.LuaJITctypeID(i)
		//pp("luar.go Dump sees ctype = %v", ctype)
		switch ctype {
		case 5: //  int8
		case 6: //  uint8
		case 7: //  int16
		case 8: //  uint16
		case 9: //  int32
		case 10: //  uint32
		case 11: //  int64
			val := L.CdataToInt64(i)
			return fmt.Sprintf(" int64: '%v'\n", val)
		case 12: //  uint64
			val := L.CdataToUint64(i)
			return fmt.Sprintf(" uint64: '%v'\n", val)
		case 13: //  float32
		case 14: //  float64

		case 0: // means it wasn't a ctype
		}

	case golua.LUA_TUSERDATA:
		return fmt.Sprintf(" Type(code %v/ LUA_TUSERDATA) : no auto-print available.\n", t)
	case golua.LUA_TFUNCTION:
		return fmt.Sprintf(" Type(code %v/ LUA_TFUNCTION) : no auto-print available.\n", t)
	default:
	}
	return fmt.Sprintf(" Type(code %v) : no auto-print available.\n", t)
}

func FetchPreludeFilenames(preludePath string, quiet bool) ([]string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	pp("FetchPrelude called on path '%s', where cwd = '%s'", preludePath, cwd)
	if !DirExists(preludePath) {
		return nil, fmt.Errorf("-prelude dir does not exist: '%s'", preludePath)
	}
	files, err := filepath.Glob(fmt.Sprintf("%s/*.lua", preludePath))
	if err != nil {
		return nil, fmt.Errorf("-prelude dir '%s' open problem: '%v'", preludePath, err)
	}
	if len(files) < 1 {
		return nil, fmt.Errorf("-prelude dir '%s' had no lua files in it.", preludePath)
	}
	// get a consisten application order, by sorting by name.
	sort.Strings(files)
	if !quiet {
		fmt.Printf("\nusing this prelude directory: '%s'\n", preludePath)
		shortFn := make([]string, len(files))
		for i, fn := range files {
			shortFn[i] = path.Base(fn)
		}
		fmt.Printf("using these files as prelude: %s\n", strings.Join(shortFn, ", "))
	}
	return files, nil
}

// prefer below LuaMustInt64
func LuaMustInt(vm *golua.State, varname string, expect int) {

	vm.GetGlobal(varname)
	top := vm.GetTop()
	value_int := vm.ToInteger(top) // lossy for 64-bit int64, use vm.CdataToInt64() instead.

	pp("LuaMustInt, expect='%v'; observe value_int='%v'", expect, value_int)
	if value_int != expect {
		DumpLuaStack(vm)
		panic(fmt.Sprintf("expected %v, got %v for '%v'", expect, value_int, varname))
	}
}

func LuaMustInt64(vm *golua.State, varname string, expect int64) {

	vm.GetGlobal(varname)
	top := vm.GetTop()
	value_int := vm.CdataToInt64(top)

	pp("LuaMustInt64, expect='%v'; observe value_int='%v'", expect, value_int)
	if value_int != expect {
		DumpLuaStack(vm)
		panic(fmt.Sprintf("expected %v, got %v for '%v'", expect, value_int, varname))
	}
}

func LuaInGlobalEnv(vm *golua.State, varname string) bool {

	vm.GetGlobal(varname)
	ret := !vm.IsNil(-1)
	vm.Pop(1)
	return ret
}

func LuaMustNotBeInGlobalEnv(vm *golua.State, varname string) {

	if LuaInGlobalEnv(vm, varname) {
		DumpLuaStack(vm)
		panic(fmt.Sprintf("expected %v to not be in global env, but it was.", varname))
	}
}

func LuaMustBeInGlobalEnv(vm *golua.State, varname string) {

	if !LuaInGlobalEnv(vm, varname) {
		DumpLuaStack(vm)
		panic(fmt.Sprintf("expected %v to be in global env, but it was not.", varname))
	}
}

func LuaMustFloat64(vm *golua.State, varname string, expect float64) {

	vm.GetGlobal(varname)
	top := vm.GetTop()
	value := vm.ToNumber(top)

	pp("LuaMustInt64, expect='%v'; observed value='%v'", expect, value)
	if math.Abs(value-expect) > 1e-8 {
		DumpLuaStack(vm)
		panic(fmt.Sprintf("expected %v, got %v for '%v'", expect, value, varname))
	}
}

func LuaMustString(vm *golua.State, varname string, expect string) {

	vm.GetGlobal(varname)
	top := vm.GetTop()
	value_string := vm.ToString(top)

	pp("value_string=%v", value_string)
	if value_string != expect {
		DumpLuaStack(vm)
		panic(fmt.Sprintf("expected %v, got value '%s' -> '%v'", expect, varname, value_string))
	}
}

func LuaMustBool(vm *golua.State, varname string, expect bool) {

	vm.GetGlobal(varname)
	top := vm.GetTop()
	value_bool := vm.ToBoolean(top)

	pp("value_bool=%v", value_bool)
	if value_bool != expect {
		DumpLuaStack(vm)
		panic(fmt.Sprintf("expected %v, got value '%s' -> '%v'", expect, varname, value_bool))
	}
}

func LuaMustBeNil(vm *golua.State, varname string) {
	isNil, alt := LuaIsNil(vm, varname)

	if !isNil {
		DumpLuaStack(vm)
		panic(fmt.Sprintf("expected varname '%s' to "+
			"be nil, but was '%s' instead.", varname, alt))
	}

}
func LuaIsNil(vm *golua.State, varname string) (bool, string) {

	vm.GetGlobal(varname)
	isNil := vm.IsNil(-1)
	top := vm.GetTop()
	vm.Pop(1)
	return isNil, LuaStackPosToString(vm, top)
}

func LuaRunAndReport(vm *golua.State, s string) {
	interr := vm.LoadString(s)
	if interr != 0 {
		fmt.Printf("error from Lua vm.LoadString(): supplied lua with: '%s'\nlua stack:\n", s)
		DumpLuaStack(vm)
		vm.Pop(1)
	} else {
		err := vm.Call(0, 0)
		if err != nil {
			fmt.Printf("error from Lua vm.Call(0,0): '%v'. supplied lua with: '%s'\nlua stack:\n", err, s)
			DumpLuaStack(vm)
			vm.Pop(1)
		}
	}
}

func dumpTableString(L *golua.State, index int) (s string) {

	// Push another reference to the table on top of the stack (so we know
	// where it is, and this function can work for negative, positive and
	// pseudo indices
	L.PushValue(index)
	// stack now contains: -1 => table
	L.PushNil()
	// stack now contains: -1 => nil; -2 => table
	for L.Next(-2) != 0 {

		// stack now contains: -1 => value; -2 => key; -3 => table
		// copy the key so that lua_tostring does not modify the original
		L.PushValue(-2)
		// stack now contains: -1 => key; -2 => value; -3 => key; -4 => table
		key := L.ToString(-1)
		value := L.ToString(-2)
		s += fmt.Sprintf("'%s' => '%s'\n", key, value)
		// pop value + copy of key, leaving original key
		L.Pop(2)
		// stack now contains: -1 => key; -2 => table
	}
	// stack now contains: -1 => table (when lua_next returns 0 it pops the key
	// but does not push anything.)
	// Pop table
	L.Pop(1)
	// Stack is now the same as it was on entry to this function
	return
}

func LuaMustRune(vm *golua.State, varname string, expect rune) {

	vm.GetGlobal(varname)
	top := vm.GetTop()
	value_int := rune(vm.CdataToInt64(top))

	pp("LuaMustRune, expect='%v'; observe value_int='%v'", expect, value_int)
	if value_int != expect {
		DumpLuaStack(vm)
		panic(fmt.Sprintf("expected %v, got %v for '%v'", expect, value_int, varname))
	}
}

func sumSliceOfInts(a []interface{}) (tot int) {
	for _, v := range a {
		switch y := v.(type) {
		case int:
			tot += y
		case int64:
			tot += int(y)
		case float64:
			tot += int(y)
		default:
			panic(fmt.Sprintf("unknown type '%T'", v))
		}
	}
	return
}

// for Test080
func sumArrayInt64(a [3]int64) (tot int64) {
	for i, v := range a {
		fmt.Printf("\n %v, sumArrayInt64 adding '%v' to tot", i, v)
		tot += v
	}
	fmt.Printf("\n sumArrayInt64 is returning tot='%v'", tot)
	return
}

//func __subslice(t, low, hi, cap) {
//
//}
