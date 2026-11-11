//go:build ios && cgo

package main

/*
#cgo LDFLAGS: -framework CoreFoundation
#include <CoreFoundation/CoreFoundation.h>
static void ag_keep_main_loop(void *info){}
static void ag_run_main_loop(void){CFRunLoopSourceContext context={0};context.perform=ag_keep_main_loop;CFRunLoopSourceRef source=CFRunLoopSourceCreate(kCFAllocatorDefault,0,&context);if(!source)return;CFRunLoopAddSource(CFRunLoopGetMain(),source,kCFRunLoopDefaultMode);CFRunLoopRun();CFRunLoopRemoveSource(CFRunLoopGetMain(),source,kCFRunLoopDefaultMode);CFRelease(source);}
*/
import "C"
import "runtime"

// 初始化时固定真正的主线程；HTTP 与脚本在其他 goroutine 执行。
func init()                        { runtime.LockOSThread() }
func runWithMainLoop(serve func()) { go serve(); C.ag_run_main_loop() }
