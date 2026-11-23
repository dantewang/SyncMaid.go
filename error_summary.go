package main

import (
	"strings"
)

// classifySyncError maps raw engine/OS diagnostics to concise localized summary
// and hint catalog keys while preserving the raw string for technical logs.
func classifySyncError(raw string) (summaryKey, hintKey string) {
	lower := strings.ToLower(strings.TrimSpace(raw))
	if lower == "" {
		return "查看日志", "请查看下方技术详情以排查具体错误原因。"
	}
	hasAny := func(needles ...string) bool {
		for _, needle := range needles {
			if strings.Contains(lower, needle) {
				return true
			}
		}
		return false
	}
	switch {
	case hasAny("不可重叠", "互相包含", "同一文件夹", "目标目录句柄已变化", "目录重叠"):
		return "源与目标目录重叠", "源目录与目标目录不能相同或互相包含（包括别名和符号链接），请修改目标路径。"
	case hasAny(
		"the system cannot find the file specified",
		"the system cannot find the path specified",
		"the network path was not found",
		"network name cannot be found",
		"no such file or directory",
		"not a directory",
		"目标文件夹未填写",
		"请选择现有文件夹的绝对路径",
		"cannot find the",
	):
		return "目录不可访问", "目标或源文件夹不存在、尚未挂载，或网络共享暂时不可达。请检查路径与网络连接。"
	case hasAny(
		"access is denied",
		"permission denied",
		"write protect",
		"sharing violation",
		"being used by another process",
		"拒绝访问",
	):
		return "权限不足", "没有足够的读取或写入权限，或文件/目录正被其他程序占用。请检查目录权限与共享权限。"
	case hasAny("不支持回收确认", "回收站暂不支持", "回收站接口不可用"):
		return "不支持回收站", "此目标所在卷或网络共享不支持回收站，请在目标编辑中切换为永久删除后再重新运行。"
	case hasAny(
		"directory identity cannot be verified",
		"unsupported installation or data location",
		"not supported",
		"暂不支持区分大小写的目录",
		"不支持链接或重解析点",
		"暂不支持硬链接文件",
		"reparse point",
		"case-sensitive",
	):
		return "目录或文件系统不受支持", "目标或源所在文件系统/网络共享不支持当前操作（如目录身份校验、大小写敏感、链接/重解析点或回收站），请查看技术详情。"
	case hasAny(
		"rust regex",
		"needs review",
		"invalid or noncanonical filter",
		"unknown filter",
		"regex",
		"过滤",
	):
		return "过滤规则未就绪", "目标的过滤规则或正则表达式尚未完成迁移确认，或包含不受支持的规则。"
	case hasAny(
		"mirror",
		"镜像",
		"未确认本次删除",
		"源没有普通文件",
		"已变化，请重新运行",
		"源文件已在扫描后改变",
		"源对象已变化",
	):
		return "镜像安全检查未通过", "出于文件安全保护，本次镜像删除或移动未能继续。请核对源目录与目标目录文件状态后重试。"
	default:
		return "查看日志", "请查看下方技术详情以排查具体错误原因。"
	}
}
