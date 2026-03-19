package repo

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// CopyDirRecursive 递归复制 src 目录到 dst。
// 如果 src 不存在，静默返回 nil。
// 如果 dst 已存在，先删除再整体复制，保证一致性。
func CopyDirRecursive(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // 源目录不存在，静默跳过
		}
		return fmt.Errorf("stat 源目录失败(%s): %w", src, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("源路径不是目录: %s", src)
	}

	// 先删除目标目录再整体复制
	if err := os.RemoveAll(dst); err != nil {
		return fmt.Errorf("清理目标目录失败(%s): %w", dst, err)
	}

	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return fmt.Errorf("计算相对路径失败: %w", err)
		}
		target := filepath.Join(dst, rel)

		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}

		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("打开源文件失败(%s): %w", src, err)
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return fmt.Errorf("获取源文件信息失败(%s): %w", src, err)
	}

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode())
	if err != nil {
		return fmt.Errorf("创建目标文件失败(%s): %w", dst, err)
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("复制文件内容失败(%s -> %s): %w", src, dst, err)
	}
	return nil
}
