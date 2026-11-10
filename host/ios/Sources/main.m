// main.m —— 程序入口
//
// 【为什么不用 storyboard / SwiftUI】
// 与参考实现一致：纯代码构界面。壳越小越可控，也不会因为 Xcode 工程文件
// 缺资源导致"编过了但运行白屏"。
#import <UIKit/UIKit.h>
#import "AGAppDelegate.h"

int main(int argc, char *argv[]) {
    @autoreleasepool {
        return UIApplicationMain(argc, argv, nil, NSStringFromClass([AGAppDelegate class]));
    }
}
