//go:build ios && cgo

#import "runtime.h"
#include <mach/mach_time.h>

// 私有入口只在运行时解析。函数声明参考公开的 WebKit IOKit SPI，未复制闭源实现。
typedef CFTypeRef Event;
typedef Event (*CreateClient)(CFAllocatorRef);
typedef Event (*CreateHand)(CFAllocatorRef,uint64_t,uint32_t,uint32_t,uint32_t,uint32_t,uint32_t,double,double,double,double,double,uint32_t,uint32_t,uint32_t);
typedef Event (*CreateFinger)(CFAllocatorRef,uint64_t,uint32_t,uint32_t,uint32_t,double,double,double,double,double,uint32_t,uint32_t,uint32_t);
typedef Event (*CreateKey)(CFAllocatorRef,uint64_t,uint32_t,uint32_t,uint32_t,uint32_t);
// Unicode 入口的六参数顺序来自参考包调用点核对，payload 为 UTF-8 字节。
typedef Event (*CreateUnicode)(CFAllocatorRef,uint64_t,const uint8_t *,CFIndex,uint32_t,uint32_t);
typedef void (*DispatchEvent)(Event,Event);
typedef void (*AppendEvent)(Event,Event,uint32_t);
typedef void (*SetInteger)(Event,uint32_t,int64_t);
typedef void (*SetSender)(Event,uint64_t);

static NSLock *inputLock;
static NSMutableDictionary *contacts;
static Event client;
static uint64_t senderID;
static CreateClient createClient;
static CreateHand createHand;
static CreateFinger createFinger;
static CreateKey createKey;
static CreateUnicode createUnicode;
static DispatchEvent dispatchEvent;
static AppendEvent appendEvent;
static SetInteger setInteger;
static SetSender setSender;

static void InitInput(void) {
    static dispatch_once_t once;
    dispatch_once(&once,^{
        inputLock = [NSLock new]; contacts = [NSMutableDictionary new];
        createClient = AGSymbol("IOHIDEventSystemClientCreate");
        createHand = AGSymbol("IOHIDEventCreateDigitizerEvent");
        createFinger = AGSymbol("IOHIDEventCreateDigitizerFingerEvent");
        createKey = AGSymbol("IOHIDEventCreateKeyboardEvent");
        createUnicode = AGSymbol("IOHIDEventCreateUnicodeEvent");
        dispatchEvent = AGSymbol("IOHIDEventSystemClientDispatchEvent");
        appendEvent = AGSymbol("IOHIDEventAppendEvent");
        setInteger = AGSymbol("IOHIDEventSetIntegerValue");
        setSender = AGSymbol("IOHIDEventSetSenderID");
    });
}
BOOL AGInputAvailable(NSString *kind) {
    InitInput();
    if (!createClient || !dispatchEvent) return NO;
    if ([kind isEqualToString:@"touch"]) return createHand && createFinger && appendEvent && setInteger;
    if ([kind isEqualToString:@"unicode"]) return createUnicode != NULL;
    return createKey != NULL;
}
static ag_ios_result SendKey(uint32_t page,uint32_t usage) {
    if (!createKey) return AGError(1,@"HID 键盘接口不可用");
    // 在提交之前同时创建 down/up，避免内存分配失败造成按键无法释放。
    Event down = createKey(kCFAllocatorDefault,mach_absolute_time(),page,usage,1,0);
    Event up = createKey(kCFAllocatorDefault,mach_absolute_time(),page,usage,0,0);
    if (!down || !up) { if (down) CFRelease(down); if (up) CFRelease(up); return AGError(3,@"无法创建键盘事件"); }
    dispatchEvent(client,down); dispatchEvent(client,up);
    CFRelease(down); CFRelease(up);
    return AGJSON(@{@"submitted":@YES});
}
static ag_ios_result SendFrame(NSArray *points) {
    if (!AGInputAvailable(@"touch")) return AGError(1,@"多指触摸接口不可用");
    if (![points isKindOfClass:[NSArray class]] || points.count > 10) return AGError(2,@"手指列表无效");
    NSMutableDictionary *next = [NSMutableDictionary dictionary];
    for (NSDictionary *p in points) {
        int finger = [p[@"id"] intValue]; double x = [p[@"x"] doubleValue], y = [p[@"y"] doubleValue];
        if (finger < 0 || finger > 9 || !isfinite(x) || !isfinite(y) || x < 0 || x > 1 || y < 0 || y > 1 || next[@(finger)]) return AGError(2,@"手指编号重复或坐标越界");
        next[@(finger)] = p;
    }
    if (!next.count && !contacts.count) return AGJSON(@{@"submitted":@NO});
    uint64_t timestamp = mach_absolute_time();
    uint32_t mask = 4;
    if (contacts.count != next.count || ![[NSSet setWithArray:contacts.allKeys] isEqualToSet:[NSSet setWithArray:next.allKeys]]) mask |= 3;
    Event parent = createHand(kCFAllocatorDefault,timestamp,3,0,0,mask,0,0,0,0,0,0,next.count > 0,next.count > 0,0x80000);
    if (!parent) return AGError(3,@"无法创建触摸父事件");
    // DigitizerIsDisplayIntegrated 字段；事件仍需系统授权，符号存在不表示注入已生效。
    setInteger(parent,0xb0019,1);
    if (senderID && setSender) setSender(parent,senderID);
    NSMutableSet *all = [NSMutableSet setWithArray:contacts.allKeys]; [all addObjectsFromArray:next.allKeys];
    for (NSNumber *finger in [[all allObjects] sortedArrayUsingSelector:@selector(compare:)]) {
        NSDictionary *old = contacts[finger], *current = next[finger];
        NSDictionary *p = current ?: old;
        BOOL down = current != nil;
        uint32_t change = (!old || !current) ? 3 : 4;
        Event child = createFinger(kCFAllocatorDefault,timestamp,finger.unsignedIntValue + 1,finger.unsignedIntValue + 1,change,[p[@"x"] doubleValue],[p[@"y"] doubleValue],0,down ? 1 : 0,0,down,down,0);
        if (!child) { CFRelease(parent); return AGError(3,@"无法创建手指事件，整帧未提交"); }
        appendEvent(parent,child,0); CFRelease(child);
    }
    dispatchEvent(client,parent); CFRelease(parent);
    [contacts setDictionary:next];
    return AGJSON(@{@"submitted":@YES});
}
ag_ios_result AGInput(NSString *op, NSDictionary *args) {
    InitInput();
    [inputLock lock];
    @try {
        if ([op isEqualToString:@"motion.configure"]) {
            if (contacts.count) return AGError(5,@"手指仍按下，不能切换事件配置");
            uint64_t value = [args[@"senderId"] unsignedLongLongValue];
            if (value && !setSender) return AGError(1,@"事件来源配置接口不可用");
            senderID = value; return AGJSON(@{});
        }
        if (!createClient || !dispatchEvent) return AGError(1,@"HID 事件服务接口不可用");
        if (!client) client = createClient(kCFAllocatorDefault);
        if (!client) return AGError(3,@"无法创建 HID 客户端，需检查授权环境");
        if ([op isEqualToString:@"motion.frame"]) return SendFrame(args[@"contacts"] ?: @[]);
        if ([op isEqualToString:@"ime.key"]) return SendKey(0x07,[args[@"usage"] unsignedIntValue]);
        if ([op isEqualToString:@"device.button"]) {
            NSString *button = args[@"button"];
            if ([button isEqualToString:@"home"]) return SendKey(0x0c,0x40);
            if ([button isEqualToString:@"power"]) return SendKey(0x0c,0x30);
            if ([button isEqualToString:@"volume_up"]) return SendKey(0x0c,0xe9);
            if ([button isEqualToString:@"volume_down"]) return SendKey(0x0c,0xea);
            return AGError(2,@"未知系统按键");
        }
        if ([op isEqualToString:@"ime.text"]) {
            if (!createUnicode) return AGError(1,@"Unicode 输入接口不可用");
            NSData *utf8 = [args[@"text"] dataUsingEncoding:NSUTF8StringEncoding];
            if (!utf8.length || utf8.length > 65536) return AGError(2,@"文本数据无效");
            Event event = createUnicode(kCFAllocatorDefault,mach_absolute_time(),utf8.bytes,utf8.length,0,0);
            if (!event) return AGError(3,@"无法创建 Unicode 事件");
            dispatchEvent(client,event); CFRelease(event);
            return AGJSON(@{@"submitted":@YES});
        }
        return AGError(1,@"未知输入操作");
    } @finally { [inputLock unlock]; }
}
