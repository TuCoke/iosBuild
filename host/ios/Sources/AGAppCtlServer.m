#import "AGAppCtlServer.h"
#import <JavaScriptCore/JavaScriptCore.h>
#include <arpa/inet.h>
#include <sys/socket.h>
#include <unistd.h>
#include <errno.h>
#include <string.h>
#include <dlfcn.h>

// JavaScriptCore 的执行限额避免同步无限循环永久占用宿主 JS 线程。
static bool AGTerminateScript(JSContextRef context,void *opaque){*(BOOL *)opaque=YES;return true;}
static const uint32_t AGControlMaxFrame = 16 * 1024 * 1024;
static BOOL AGReadAll(int fd,void *bytes,size_t count) {
    uint8_t *p=bytes;
    while(count){ssize_t n=read(fd,p,count);if(n<0&&errno==EINTR)continue;if(n<=0)return NO;p+=n;count-=(size_t)n;}return YES;
}
static BOOL AGWriteAll(int fd,const void *bytes,size_t count) {
    const uint8_t *p=bytes;
    while(count){ssize_t n=write(fd,p,count);if(n<0&&errno==EINTR)continue;if(n<=0)return NO;p+=n;count-=(size_t)n;}return YES;
}
static NSData *AGControlString(NSString *value) {
    NSData *text=[(value ?: @"") dataUsingEncoding:NSUTF8StringEncoding];
    uint32_t size=htonl((uint32_t)text.length);
    NSMutableData *data=[NSMutableData dataWithBytes:&size length:4];[data appendData:text];return data;
}
static NSString *AGTakeControlString(NSData *data,NSUInteger *offset) {
    if(*offset>data.length || data.length-*offset<4)return nil;
    uint32_t size;memcpy(&size,(const uint8_t *)data.bytes+*offset,4);*offset+=4;size=ntohl(size);
    if(size>data.length-*offset)return nil;
    NSString *out=[[NSString alloc] initWithBytes:(const uint8_t *)data.bytes+*offset length:size encoding:NSUTF8StringEncoding];*offset+=size;return out;
}
@interface AGAppCtlConnection : NSObject
@property(nonatomic,assign) int fd;
@property(nonatomic,assign) BOOL closed;
@property(nonatomic,strong) dispatch_queue_t jsQueue;
@property(nonatomic,strong) NSMutableDictionary<NSString *,JSContext *> *contexts;
- (void)run;
- (BOOL)sendStatus:(uint32_t)status messageID:(uint32_t)messageID payload:(NSData *)payload;
- (void)close;
@end
@implementation AGAppCtlConnection
- (instancetype)init {if((self=[super init])){_fd=-1;_jsQueue=dispatch_queue_create("cc.agov.apkctl.javascript",DISPATCH_QUEUE_SERIAL);_contexts=[NSMutableDictionary dictionary];}return self;}
- (BOOL)sendStatus:(uint32_t)status messageID:(uint32_t)messageID payload:(NSData *)payload {
    if(payload.length>AGControlMaxFrame-4)return NO;
    uint32_t header[]={htonl(messageID),htonl((uint32_t)payload.length+4),htonl(status)};
    @synchronized(self){if(self.closed)return NO;
      BOOL ok=AGWriteAll(self.fd,header,sizeof(header)) && AGWriteAll(self.fd,payload.bytes,payload.length);
      if(!ok)[self close];return ok;}
}
- (void)close { @synchronized(self){if(self.closed)return;self.closed=YES;shutdown(self.fd,SHUT_RDWR);close(self.fd);self.fd=-1;} }
- (void)run {
    for(;;){@autoreleasepool {
        uint32_t header[2];if(!AGReadAll(self.fd,header,8))break;
        uint32_t messageID=ntohl(header[0]),length=ntohl(header[1]);
        if(messageID==0 || length<4 || length>AGControlMaxFrame)break;
        NSMutableData *body=[NSMutableData dataWithLength:length];if(!AGReadAll(self.fd,body.mutableBytes,length))break;
        uint32_t status;memcpy(&status,body.bytes,4);status=ntohl(status);
        if(status!=1){[self sendStatus:3 messageID:messageID payload:AGControlString(@"未知宿主控制命令")];continue;}
        NSUInteger offset=4;NSString *contextID=AGTakeControlString(body,&offset);NSString *source=AGTakeControlString(body,&offset);
        if(!contextID || !source || offset!=body.length){[self sendStatus:3 messageID:messageID payload:AGControlString(@"控制帧字符串无效")];continue;}
        // 每个连接独立保存 JS 上下文；断开后释放，下一次脚本不会继承旧全局变量。
        dispatch_async(self.jsQueue,^{@autoreleasepool {
            @synchronized(self){if(self.closed)return;}
            JSContext *context=self.contexts[contextID];
            if(!context){
                context=[[JSContext alloc] init];self.contexts[contextID]=context;
                __weak AGAppCtlConnection *weakSelf=self;
                context[@"sendToScript"]=^(NSString *message){
                    AGAppCtlConnection *connection=weakSelf;
                    NSMutableData *payload=[NSMutableData dataWithData:AGControlString(contextID)];[payload appendData:AGControlString(message)];
                    [connection sendStatus:5 messageID:1 payload:payload];
                };
            }
            context.exception=nil;
            typedef void (*SetLimit)(JSContextGroupRef,double,bool(*)(JSContextRef,void *),void *);
            typedef void (*ClearLimit)(JSContextGroupRef);
            SetLimit setLimit=(SetLimit)dlsym(RTLD_DEFAULT,"JSContextGroupSetExecutionTimeLimit");
            ClearLimit clearLimit=(ClearLimit)dlsym(RTLD_DEFAULT,"JSContextGroupClearExecutionTimeLimit");
            BOOL terminated=NO;JSContextGroupRef group=JSContextGetGroup(context.JSGlobalContextRef);
            if(!setLimit || !clearLimit){[self sendStatus:3 messageID:messageID payload:AGControlString(@"JavaScriptCore 执行限额入口不可用")];return;}
            setLimit(group,30,AGTerminateScript,&terminated);
            JSValue *result=[context evaluateScript:source withSourceURL:[NSURL URLWithString:@"agov://apkctl/eval.js"]];
            clearLimit(group);
            NSString *error=terminated ? @"JavaScriptCore 执行超时" : (context.exception ? [context.exception toString] : nil);
            NSString *text=(!result || result.isUndefined || result.isNull) ? @"" : [result toString];
            [self sendStatus:error ? 3 : 0 messageID:messageID payload:AGControlString(error ?: text)];
        }});
    }}
    [self close];
    dispatch_async(self.jsQueue,^{[self.contexts removeAllObjects];});
}
@end

@implementation AGAppCtlServer {
    int _listener;
    NSMutableSet<AGAppCtlConnection *> *_clients;
}
+ (instancetype)shared {static AGAppCtlServer *instance;static dispatch_once_t once;dispatch_once(&once,^{instance=[AGAppCtlServer new];});return instance;}
- (instancetype)init {if((self=[super init])){_listener=-1;_clients=[NSMutableSet set];}return self;}
- (BOOL)start:(NSError **)error {
 @synchronized(self){
    if(_listener>=0)return YES;
    int fd=socket(AF_INET,SOCK_STREAM,0);if(fd<0)goto failed;
    int yes=1;setsockopt(fd,SOL_SOCKET,SO_REUSEADDR,&yes,sizeof(yes));
    struct sockaddr_in address={0};address.sin_len=sizeof(address);address.sin_family=AF_INET;address.sin_port=htons(8990);address.sin_addr.s_addr=htonl(INADDR_LOOPBACK);
    if(bind(fd,(struct sockaddr *)&address,sizeof(address))!=0 || listen(fd,8)!=0){int saved=errno;close(fd);errno=saved;goto failed;}
    _listener=fd;
    dispatch_async(dispatch_get_global_queue(QOS_CLASS_UTILITY,0),^{
      for(;;){int client=accept(fd,NULL,NULL);if(client<0){if(errno==EINTR)continue;break;}
        int yes=1;setsockopt(client,SOL_SOCKET,SO_NOSIGPIPE,&yes,sizeof(yes));
        struct timeval timeout={5,0};setsockopt(client,SOL_SOCKET,SO_SNDTIMEO,&timeout,sizeof(timeout));
        AGAppCtlConnection *connection=[AGAppCtlConnection new];connection.fd=client;
        @synchronized(self){if(self->_clients.count>=8){[connection close];continue;}[self->_clients addObject:connection];}
        dispatch_async(dispatch_get_global_queue(QOS_CLASS_UTILITY,0),^{[connection run];@synchronized(self){[self->_clients removeObject:connection];}});
      }
    });
    return YES;
failed:
    if(error)*error=[NSError errorWithDomain:NSPOSIXErrorDomain code:errno userInfo:nil];return NO;
 }
}
- (void)emitLifecycle:(NSString *)name {
    NSMutableData *payload=[NSMutableData dataWithData:AGControlString(@"__autogo_lifecycle__")];[payload appendData:AGControlString(name)];
    NSArray *clients;@synchronized(self){clients=_clients.allObjects;}
    for(AGAppCtlConnection *client in clients)[client sendStatus:5 messageID:1 payload:payload];
}
@end
