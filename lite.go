//go:build lite

// 省空间部署版（lite）：不内嵌 adb/scrcpy，运行时扫描系统与缓存，
// 缺依赖时从官方源下载并校验 SHA256，部署到用户缓存目录后复用。
package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	scrcpyTarName = "scrcpy-linux-x86_64-v" + scrcpyVersion + ".tar.gz"
	scrcpyTarDir  = "scrcpy-linux-x86_64-v" + scrcpyVersion
	scrcpyURLBase = "https://github.com/Genymobile/scrcpy/releases/download/v" + scrcpyVersion
)

func scrcpyURL() string {
	// AMB_SCRCPY_URL 优先；旧名 ADBCTL_SCRCPY_URL 仍兼容
	if v := os.Getenv("AMB_SCRCPY_URL"); v != "" {
		return v
	}
	if v := os.Getenv("ADBCTL_SCRCPY_URL"); v != "" {
		return v
	}
	return scrcpyURLBase + "/" + scrcpyTarName
}

func scrcpySumsURL() string {
	// AMB_SCRCPY_SUMS_URL 优先；旧名 ADBCTL_SCRCPY_SUMS_URL 仍兼容
	if v := os.Getenv("AMB_SCRCPY_SUMS_URL"); v != "" {
		return v
	}
	if v := os.Getenv("ADBCTL_SCRCPY_SUMS_URL"); v != "" {
		return v
	}
	return scrcpyURLBase + "/SHA256SUMS.txt"
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// scanDeps 只扫描，不下载。
func scanDeps() depsReport {
	root := cacheHome()
	bin := filepath.Join(root, "bin")
	rep := depsReport{cacheRoot: root}
	rep.sysAdb, _ = exec.LookPath("adb")
	rep.sysScrcpy, _ = exec.LookPath("scrcpy")
	if fileExists(filepath.Join(bin, "adb")) {
		rep.cacheAdb = filepath.Join(bin, "adb")
	}
	if fileExists(filepath.Join(bin, "scrcpy")) {
		rep.cacheScrcpy = filepath.Join(bin, "scrcpy")
	}
	return rep
}

// resolveExternal 按「缓存 -> 系统 -> 下载」决定 adb/scrcpy 来源。
func resolveExternal() error {
	if isWindows() {
		return fmt.Errorf("省空间部署版（lite）目前只提供 Linux；Windows 请用内嵌单文件版")
	}
	root := cacheHome()
	bin := filepath.Join(root, "bin")
	cacheAdb := filepath.Join(bin, "adb")
	cacheScrcpy := filepath.Join(bin, "scrcpy")
	cacheServer := filepath.Join(bin, "scrcpy-server")

	rep := scanDeps()
	sysAdb := rep.sysAdb
	if v := os.Getenv("ADB"); v != "" {
		sysAdb = v
	}
	sysScrcpy := rep.sysScrcpy
	if v := os.Getenv("SCRCPY"); v != "" {
		sysScrcpy = v
	}

	// 1) 缓存里已部署过：直接用，可离线
	if rep.cacheAdb != "" && rep.cacheScrcpy != "" {
		fmt.Fprintf(os.Stderr, "amb: 使用已部署的依赖：%s\n", bin)
		payloadRoot = root
		payloadBinDir = bin
		adbPath, scrcpyPath, serverPath = cacheAdb, cacheScrcpy, cacheServer
		return nil
	}

	// 2) 系统两个都有：直接用，不下载、不占额外空间
	if sysAdb != "" && sysScrcpy != "" {
		fmt.Fprintln(os.Stderr, "amb: 检测到系统中已有 adb 与 scrcpy，直接使用（不下载、不占额外空间）。")
		adbPath, scrcpyPath = sysAdb, sysScrcpy
		serverPath = ""
		payloadRoot = ""
		payloadBinDir = ""
		return nil
	}

	// 3) 缺依赖：下载官方 scrcpy 包（自带 adb + scrcpy + scrcpy-server）
	fmt.Fprintf(os.Stderr, "amb: 依赖不完整（adb=%s，scrcpy=%s）\n", orNone(sysAdb), orNone(sysScrcpy))
	fmt.Fprintf(os.Stderr, "amb: 首次使用会下载官方 scrcpy v%s（约 18MB）并校验 SHA256，之后复用。\n", scrcpyVersion)
	if err := deployScrcpyBundle(root); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "amb: 依赖已部署完成。")
	adbPath, scrcpyPath, serverPath = cacheAdb, cacheScrcpy, cacheServer
	if sysAdb != "" && fileExists(sysAdb) {
		adbPath = sysAdb
	}
	payloadRoot = root
	payloadBinDir = bin
	return nil
}

// deployScrcpyBundle 下载官方 scrcpy tar.gz，校验 SHA256 后把 scrcpy/adb/server 解压到 root/bin。
func deployScrcpyBundle(root string) error {
	parent := filepath.Dir(root)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(parent, "amb-dl-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	sumsPath := filepath.Join(tmp, "SHA256SUMS.txt")
	tarPath := filepath.Join(tmp, scrcpyTarName)

	fmt.Fprintln(os.Stderr, "amb:   [1/4] 下载校验清单 …")
	if err := downloadFile(scrcpySumsURL(), sumsPath); err != nil {
		return fmt.Errorf("下载 SHA256SUMS.txt 失败：%w", err)
	}
	fmt.Fprintf(os.Stderr, "amb:   [2/4] 下载 %s …\n", scrcpyTarName)
	if err := downloadFile(scrcpyURL(), tarPath); err != nil {
		return fmt.Errorf("下载 scrcpy 包失败：%w", err)
	}

	fmt.Fprintln(os.Stderr, "amb:   [3/4] 校验 SHA256 …")
	want, err := expectedSHA256(sumsPath, scrcpyTarName)
	if err != nil {
		return err
	}
	got, err := fileSHA256(tarPath)
	if err != nil {
		return err
	}
	if !strings.EqualFold(want, got) {
		return fmt.Errorf("scrcpy 包 SHA256 校验失败：期望 %s，实际 %s（已丢弃，请重试）", want, got)
	}

	fmt.Fprintln(os.Stderr, "amb:   [4/4] 解压到缓存 …")
	stage, err := os.MkdirTemp(parent, "amb-stage-")
	if err != nil {
		return err
	}
	if err := extractScrcpyTar(tarPath, filepath.Join(stage, "bin")); err != nil {
		os.RemoveAll(stage)
		return err
	}
	_ = os.WriteFile(filepath.Join(stage, ".extracted-"+appVersion), []byte("ok\n"), 0o644)
	_ = os.RemoveAll(root)
	if err := os.Rename(stage, root); err != nil {
		os.RemoveAll(stage)
		return err
	}
	return nil
}

func extractScrcpyTar(tarPath, destBin string) error {
	f, err := os.Open(tarPath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		slash := filepath.ToSlash(hdr.Name)
		if !strings.HasPrefix(slash, scrcpyTarDir+"/") {
			continue
		}
		rel := strings.TrimPrefix(slash, scrcpyTarDir+"/")
		if strings.Contains(rel, "/") {
			continue
		}
		switch rel {
		case "scrcpy", "scrcpy-server", "adb":
			if err := writeReader(filepath.Join(destBin, rel), tr, 0o755); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeReader(target string, r io.Reader, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(target, mode)
}

func downloadFile(url, dest string) error {
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		lastErr = downloadOnce(url, dest)
		if lastErr == nil {
			return nil
		}
		if attempt < 3 {
			fmt.Fprintf(os.Stderr, "amb:   下载中断（%v），重试 %d/3 …\n", lastErr, attempt+1)
			time.Sleep(time.Duration(attempt) * 2 * time.Second)
		}
	}
	return lastErr
}

// downloadOnce 支持断点续传：dest 已存在时带 Range 头继续下载。
func downloadOnce(url, dest string) error {
	var offset int64
	if st, err := os.Stat(dest); err == nil {
		offset = st.Size()
	}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "amb/"+appVersion)
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	client := &http.Client{Timeout: 60 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if offset > 0 && resp.StatusCode == http.StatusOK {
		offset = 0 // 服务器不支持续传，从头来
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("HTTP %s", resp.Status)
	}

	flags := os.O_CREATE | os.O_WRONLY
	if offset == 0 {
		flags |= os.O_TRUNC
	}
	out, err := os.OpenFile(dest, flags, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	if offset > 0 {
		if _, err := out.Seek(offset, io.SeekStart); err != nil {
			return err
		}
	}
	_, err = io.Copy(out, resp.Body)
	return err
}

func expectedSHA256(sumsPath, name string) (string, error) {
	data, err := os.ReadFile(sumsPath)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == name {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("校验清单里找不到 %s", name)
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
