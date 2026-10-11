# ChatGPT / LocalGate 文件交接验收

这套流程针对 ChatGPT MCP Apps，不使用 OpenAI API Containers，也不能假定两者的 Sandbox 共享文件。

## 必须区分的阶段

1. LocalGate export_file 返回 export_id、size、sha256、有效期和临时签名 HTTPS 下载地址。
2. Widget 获取原始字节并校验源文件后，通过 window.openai.uploadFile(file, { library: true }) 上传，拿到真实 fileId。
3. Widget 通过 getFileDownloadUrl 获取 ChatGPT 文件地址后，实际下载并比较原件与回读文件的大小和 SHA-256。仅获取 URL 不算验证通过。
4. Widget State 保存模型摘要、UI 私有信息和合法的 imageIds；重新渲染时恢复文件标识，避免同一导出重复上传。
5. **模型视觉上下文可见、当前会话文件列表可见、Python Sandbox 可读是三个独立检查项**。拿到 fileId 不等于文件已进入当前会话的 /mnt/data。

## 自动化测试

在 LocalGate 仓库执行：

    node --test internal/mcpgo/ui/artifact-viewer.test.mjs
    go test -p 1 ./internal/mcpgo ./internal/fileexport
    git diff --check

Widget 自动化测试使用模拟 ChatGPT Host，可覆盖文件读取、SHA 校验、上传、回读失败、状态恢复和手动下一轮验证请求，但不能证明真实 ChatGPT 已挂载文件。

## 真实 ChatGPT 端到端验收

1. 更新部署 LocalGate，并在 ChatGPT 插件详情刷新 MCP 连接；核对 tools/list 和 resources/read 的 UI URI 均为 ui://localgate/artifact-viewer/v6.html。
2. 调用 export_file，导出一张小于 32 MiB 的 PNG。记录导出结果中的原始 size、sha256 和 export_id。
3. 查看 Widget：源文件读取、上传、回读、内容一致性应分别展示结果。若提示“未验证”，先排查接口是否可用、下载签名是否过期、iframe CORS、文件大小和 SHA-256。
4. 点击“让 ChatGPT 检查图片与 Sandbox 可见性”按钮，触发下一轮模型对话。请模型分别说明：是否看见图片、是否能在当前聊天文件列表定位原图、Python 是否确实打开了原始文件。
5. 若能在 Sandbox 找到原文件，再使用 Pillow 打开、计算 SHA-256，与 LocalGate 原文件校验并生成直方图。若没有实际可读路径，应如实记录“不支持/未挂载”，不可根据 fileId 猜测路径。
6. 重新打开同一个 Widget：应恢复 fileId、诊断结果；在相同 export_id 下不会重新上传。新的 export_file 调用会得到新 export_id，应重新检查。

## 故障定位

| 观察到的情况 | 说明 |
| --- | --- |
| 不显示 Widget | 核对 MCP 工具的 ui.resourceUri、资源可读性和连接器缓存 |
| 源文件校验失败 | 检查 LocalGate 下载和 CORS，及 size/SHA-256 是否一致 |
| uploadFile 返回 fileId | 上传成功，但不证明文件已持久化到 Library 或挂载 Sandbox |
| 回读地址存在但 fetch 失败 | 不能确认文件内容；可能是 iframe CORS，已取得的 fileId 仍保留 |
| 回读 SHA-256 与源文件一致 | ChatGPT 文件回读验证通过，但 Sandbox 仍须另验 |
| 后续模型能看到图片 | imageIds 上下文交接成功，不等于 Python 文件路径存在 |
| Sandbox 能读取原始文件 | 可以使用 Python 验证哈希和运行像素分析，才算分析闭环 |

安全约束：不在 Widget State、日志或后续消息中传播临时签名 URL、源文件字节与本机绝对路径；超过 32 MiB 或浏览器无 Web Crypto 时明确标记未完全验证；用户点击按钮前不自动发出后续对话。
