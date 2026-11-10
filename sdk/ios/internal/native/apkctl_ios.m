//go:build ios && cgo

// 外壳（宿主 App）与脚本之间的控制通道。
//
// 三个动作：
//   apkctl.eval       外壳请求脚本执行一段脚本代码 —— 需要内置 JS 引擎（参考实现用 rhino）。
//                     我们暂未内置引擎，这里如实报不可用，绝不返回空串冒充"执行成功"。
//   apkctl.reg_event  登记要监听的生命周期事件（pause/resume/stop）
//   apkctl.poll       取回期间累积的事件与外壳消息（Go 侧每 300ms 拉一次）
//
// 事件来源：目前只有本进程内的"暂停/恢复/停止"标记（由外壳通过 setEvent 设置），
// 后续接通知中心或 URL Scheme 时只需往同一个队列里追加。
#import "runtime.h"

static NSMutableArray *AGAPKCEvents(void) {
    static NSMutableArray *events;
    static dispatch_once_t once;
    dispatch_once(&once, ^{ events = [NSMutableArray new]; });
    return events;
}

static NSMutableArray *AGAPKCMessages(void) {
    static NSMutableArray *messages;
    static dispatch_once_t once;
    dispatch_once(&once, ^{ messages = [NSMutableArray new]; });
    return messages;
}

// AGAPKCPushEvent 供外壳在收到系统事件时调用（也用于测试）。
void AGAPKCPushEvent(NSString *event) {
    if (event.length == 0) return;
    @synchronized (AGAPKCEvents()) {
        [AGAPKCEvents() addObject:event];
    }
}

// AGAPKCPushMessage 供外壳把消息投递给脚本。
void AGAPKCPushMessage(NSString *contextID, NSString *message) {
    if (message.length == 0) return;
    @synchronized (AGAPKCMessages()) {
        [AGAPKCMessages() addObject:@{@"contextId": contextID ?: @"", @"message": message}];
    }
}

ag_ios_result AGAPKCtl(NSString *op, NSDictionary *args) {
    if ([op isEqualToString:@"apkctl.eval"]) {
        NSString *script = args[@"script"];
        if (script.length == 0) return AGError(2,@"缺少脚本内容");
        // 没有内置 JS 引擎：明确失败，避免调用方以为脚本跑过了。
        return AGError(1,@"脚本引擎未接入：当前构建不含 JS 运行时");
    }
    if ([op isEqualToString:@"apkctl.reg_event"]) {
        return AGJSON(@{@"registered":@YES});
    }
    if ([op isEqualToString:@"apkctl.poll"]) {
        NSArray *events = nil;
        NSDictionary *message = nil;
        @synchronized (AGAPKCEvents()) {
            events = [AGAPKCEvents() copy];
            [AGAPKCEvents() removeAllObjects];
        }
        @synchronized (AGAPKCMessages()) {
            if (AGAPKCMessages().count) {
                message = [AGAPKCMessages().firstObject retain];
                [AGAPKCMessages() removeObjectAtIndex:0];
            }
        }
        NSDictionary *result = @{
            @"events": events ?: @[],
            @"contextId": message[@"contextId"] ?: @"",
            @"message": message[@"message"] ?: @"",
        };
        [events release];
        [message release];
        return AGJSON(result);
    }
    return AGError(1,@"未知外壳操作");
}
