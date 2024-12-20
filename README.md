
<p align="center">
 <img src="https://img.shields.io/badge/golang->1.18-blue" alt="Downloads" />
 <img src="https://img.shields.io/badge/MIT-green" alt="Downloads" />
</p>
起因是因为使用各种开源大模型已经很久了，发现很多开源大模型部署很困难，要不是界面bug很多，或者只能是以黑框运行，
我就想做一个非常傻瓜的软件，以便大家都能无痛上手，体验科技进步带来的快乐和便捷。考虑到网页方式实现可以很简单的多端使用，
通过Golang又可以实现跨平台部署，于是就用了几天使用了Golang写了一个简单的网页来调取本地的ollama。

* 支持llama3.2的图片识别
* 支持自动读取已经导入ollama全部的模型
* 不依赖任何外部中间件
* 服务依赖ollama，需要在本地正常运行ollama
* 多端支持，多端适配
* 支持在macos、windwos、linux运行

## Windwos

1、下载项目static文件夹与可执行文件放在统一路径
2、下载可执行exe文件
3、双击运行可执行文件

## Macos
1、下载项目static文件夹与可执行文件放在统一路径
2、下载可执行文件
3、Terminal运行可执行文件

## Linux
1、下载项目static文件夹与可执行文件放在统一路径
2、下载可执行文件
3、Terminal运行可执行文件