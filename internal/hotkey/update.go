package hotkey

import (
	"errors"
	"fmt"
)

type Registry interface {
	Register(string, func()) error
	Unregister(string) error
	GetAll() []string
}

// Replace runs on the UI thread. A physical shortcut retained in the new set
// stays registered, so exchanging the two purposes does not cause conflicts.
// saved is true even if releasing an obsolete binding fails after saving.
func Replace(reg Registry, desired []string, fired func(Combination), save func() error) (saved bool, err error) {
	wanted := map[Combination]bool{}
	for _, raw := range desired {
		key, err := Parse(raw)
		if err != nil {
			return false, err
		}
		if wanted[key] {
			return false, errors.New("两种用途的快捷键不能相同")
		}
		wanted[key] = true
	}
	old := map[Combination]bool{}
	for _, raw := range reg.GetAll() {
		key, err := Parse(raw)
		if err != nil {
			return false, errors.New("已有快捷键无法识别；请重启程序")
		}
		old[key] = true
	}
	added := []Combination{}
	rollback := func(cause error) (bool, error) {
		failed := false
		for _, key := range added {
			if reg.Unregister(key.Accelerator()) != nil {
				failed = true
			}
		}
		if failed {
			return false, errors.New("快捷键更新失败且回滚失败；旧配置保留，请重启程序")
		}
		return false, cause
	}
	for _, raw := range desired {
		key, _ := Parse(raw)
		if old[key] {
			continue
		}
		if err := reg.Register(key.Accelerator(), func() { fired(key) }); err != nil {
			return rollback(fmt.Errorf("快捷键 %s 无法注册或已被占用；旧配置保留", raw))
		}
		added = append(added, key)
	}
	if err := save(); err != nil {
		return rollback(err)
	}
	failed := false
	for key := range old {
		if !wanted[key] && reg.Unregister(key.Accelerator()) != nil {
			failed = true
		}
	}
	if failed {
		return true, errors.New("设置已保存但旧快捷键解绑失败；请重启程序")
	}
	return true, nil
}
