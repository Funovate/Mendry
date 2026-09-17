// Package application 实现本地密码认证、服务端 Session 和角色授权用例。
// 该包只通过窄接口访问持久化实现，且不会向调用方暴露 password hash 或 raw token。
package application
