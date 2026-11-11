// OpenCV 与样本一样走原生库。令牌注册表负责 Mat 生命周期，跨边界只传 JSON 与独立字节。
#import "ag_cv.h"
#import <TargetConditionals.h>
#import <ImageIO/ImageIO.h>
#include <mutex>
#include <unordered_map>
#include <string>
#include <cmath>
#if !TARGET_OS_SIMULATOR && __has_include(<opencv2/core.hpp>)
#include <opencv2/core.hpp>
#include <opencv2/imgproc.hpp>
#include <opencv2/imgcodecs.hpp>
#define AG_HAS_CV 1
#elif !TARGET_OS_SIMULATOR
#error "Missing OpenCV 4.10 headers for enabled iOS image capabilities"
#else
#define AG_HAS_CV 0
#endif

#if AG_HAS_CV
static std::mutex cvLock;
static std::unordered_map<std::string,cv::Mat> matrices;
static size_t retainedBytes=0;
static bool validSize(int w,int h) { return w>0 && h>0 && w<=8192 && h<=8192 && (int64_t)w*h<=8*1024*1024; }
static ag_ios_result retainMat(const cv::Mat &mat) {
    size_t bytes=mat.total()*mat.elemSize();
    if(mat.empty() || !validSize(mat.cols,mat.rows))return AGError(2,@"OpenCV 图像尺寸无效");
    if(matrices.size()>=128 || bytes>128*1024*1024-retainedBytes)return AGError(5,@"OpenCV 图像内存已达限制，请关闭不再使用的 Mat");
    NSString *token=NSUUID.UUID.UUIDString; matrices.emplace(token.UTF8String,mat); retainedBytes+=bytes;
    return AGJSON(@{@"id":token});
}
static cv::Mat asGray(const cv::Mat &mat) { cv::Mat out; if(mat.channels()==1)return mat; cv::cvtColor(mat,out,mat.channels()==4?cv::COLOR_BGRA2GRAY:cv::COLOR_BGR2GRAY);return out; }
static cv::Mat asColor(const cv::Mat &mat) { cv::Mat out;if(mat.channels()==3)return mat;cv::cvtColor(mat,out,mat.channels()==4?cv::COLOR_BGRA2BGR:cv::COLOR_GRAY2BGR);return out; }
#endif

ag_ios_result AGOpenCV(NSString *op,NSDictionary *args) {
#if AG_HAS_CV
 std::lock_guard<std::mutex> lock(cvLock);
 try {
    if([op isEqualToString:@"opencv.decode"] || [op isEqualToString:@"opencv.rgba"]) {
        NSString *encoded=args[@"base64"];if(![encoded isKindOfClass:NSString.class] || encoded.length>46*1024*1024)return AGError(2,@"图片输入无效");
        NSData *data=[[NSData alloc] initWithBase64EncodedString:encoded options:0];if(!data)return AGError(2,@"图片 Base64 无效");
        cv::Mat mat;
        if([op isEqualToString:@"opencv.rgba"]) {
            int w=[args[@"width"] intValue],h=[args[@"height"] intValue];
            if(!validSize(w,h) || data.length!=(size_t)w*h*4)return AGError(2,@"RGBA 尺寸不符");
            cv::Mat rgba(h,w,CV_8UC4,(void *)data.bytes);cv::cvtColor(rgba,mat,cv::COLOR_RGBA2BGRA);
        } else {
            // 先读取尺寸再解码，防止压缩图片导致过量内存分配。
            CGImageSourceRef source=CGImageSourceCreateWithData((__bridge CFDataRef)data,NULL);
            if(!source)return AGError(2,@"图片格式无效");
            NSDictionary *props=CFBridgingRelease(CGImageSourceCopyPropertiesAtIndex(source,0,NULL));CFRelease(source);
            if(!validSize([props[(__bridge NSString *)kCGImagePropertyPixelWidth] intValue],[props[(__bridge NSString *)kCGImagePropertyPixelHeight] intValue]))return AGError(2,@"图片尺寸过大");
            cv::Mat bytes(1,(int)data.length,CV_8U,(void *)data.bytes);mat=cv::imdecode(bytes,cv::IMREAD_UNCHANGED);
            if(mat.depth()!=CV_8U)return AGError(2,@"当前图像接口仅支持 8 位像素");
        }
        return retainMat(mat);
    }
    NSString *token=args[@"id"];if(![token isKindOfClass:NSString.class])return AGError(2,@"缺少 Mat 令牌");
    auto entry=matrices.find(token.UTF8String);
    if([op isEqualToString:@"opencv.close"]) { if(entry!=matrices.end()){retainedBytes-=entry->second.total()*entry->second.elemSize();matrices.erase(entry);}return AGJSON(@{}); }
    if(entry==matrices.end())return AGError(7,@"Mat 已关闭或不属于当前进程");
    const cv::Mat &src=entry->second;cv::Mat out;
    if([op isEqualToString:@"opencv.info"])return AGJSON(@{@"width":@(src.cols),@"height":@(src.rows),@"channels":@(src.channels()),@"depth":@(src.depth()),@"bytes":@(src.total()*src.elemSize())});
    if([op isEqualToString:@"opencv.encode"]) {
        NSString *format=args[@"format"];int quality=[args[@"quality"] intValue];if(!([format isEqual:@"png"] || [format isEqual:@"jpeg"]) || quality<1 || quality>100)return AGError(2,@"编码参数无效");
        std::vector<uchar> bytes;cv::imencode([format isEqual:@"png"]?".png":".jpg",src,bytes,{cv::IMWRITE_JPEG_QUALITY,quality});
        NSData *data=[NSData dataWithBytes:bytes.data() length:bytes.size()];return AGJSON(@{@"base64":[data base64EncodedStringWithOptions:0]});
    }
    if([op isEqualToString:@"opencv.match"]) {
        NSString *target=args[@"template"];if(![target isKindOfClass:NSString.class])return AGError(2,@"缺少模板令牌");
        auto found=matrices.find(target.UTF8String);if(found==matrices.end())return AGError(7,@"模板已关闭");
        NSDictionary *options=args[@"options"];if(![options isKindOfClass:NSDictionary.class])return AGError(2,@"匹配参数无效");
        int limit=[options[@"limit"] intValue];double threshold=[options[@"threshold"] doubleValue];
        if(limit<1 || limit>1000 || !(threshold>0 && threshold<=1))return AGError(2,@"匹配阈值或数量无效");
        cv::Mat image=asColor(src),templ=asColor(found->second),mask;
        if(templ.cols>image.cols || templ.rows>image.rows)return AGJSON(@{@"matches":@[]});
        if([options[@"transparent"] boolValue]) {
            mask=cv::Mat(templ.size(),CV_8U,cv::Scalar(255));cv::Vec3b key=templ.at<cv::Vec3b>(0,0);
            for(int y=0;y<templ.rows;y++)for(int x=0;x<templ.cols;x++) {
                cv::Vec3b pixel=templ.at<cv::Vec3b>(y,x);
                if(pixel==key || (found->second.channels()==4 && found->second.at<cv::Vec4b>(y,x)[3]==0))mask.at<uchar>(y,x)=0;
            }
            if(cv::countNonZero(mask)==0)return AGError(2,@"模板没有有效的不透明像素");
        }
        if([options[@"gray"] boolValue]){image=asGray(image);templ=asGray(templ);}
        cv::Mat scores;cv::matchTemplate(image,templ,scores,cv::TM_SQDIFF,mask);
        // 按有效像素的最大平方差归一化，全黑/常量模板也有明确分数。
        double pixels=mask.empty()?templ.total():cv::countNonZero(mask);scores/=pixels*templ.channels()*255.0*255.0;
        for(int y=0;y<scores.rows;y++)for(int x=0;x<scores.cols;x++){float &v=scores.at<float>(y,x);if(!std::isfinite(v))v=1.f;}
        NSMutableArray *matches=[NSMutableArray array];
        for(int i=0;i<limit;i++) {
            double low;cv::Point point;cv::minMaxLoc(scores,&low,NULL,&point,NULL);
            double score=std::max(0.0,std::min(1.0,1.0-low));if(score<threshold)break;
            [matches addObject:@{@"x":@(point.x),@"y":@(point.y),@"width":@(templ.cols),@"height":@(templ.rows),@"score":@(score)}];
            cv::Rect suppress(std::max(0,point.x-templ.cols+1),std::max(0,point.y-templ.rows+1),0,0);
            suppress.width=std::min(scores.cols,point.x+templ.cols)-suppress.x;suppress.height=std::min(scores.rows,point.y+templ.rows)-suppress.y;scores(suppress).setTo(1.f);
        }
        return AGJSON(@{@"matches":matches});
    }
    if([op isEqualToString:@"opencv.clone"])out=src.clone();
    else if([op isEqualToString:@"opencv.gray"])out=asGray(src).clone();
    else if([op isEqualToString:@"opencv.crop"]) {
        int x=[args[@"x"] intValue],y=[args[@"y"] intValue],w=[args[@"width"] intValue],h=[args[@"height"] intValue];
        if(x<0 || y<0 || w<=0 || h<=0 || (int64_t)x+w>src.cols || (int64_t)y+h>src.rows)return AGError(2,@"裁剪区域越界");out=src(cv::Rect(x,y,w,h)).clone();
    } else if([op isEqualToString:@"opencv.resize"]) {
        int w=[args[@"width"] intValue],h=[args[@"height"] intValue];if(!validSize(w,h))return AGError(2,@"缩放尺寸无效");cv::resize(src,out,cv::Size(w,h),0,0,cv::INTER_LINEAR);
    } else if([op isEqualToString:@"opencv.rotate"]) {
        int turns=[args[@"turns"] intValue];if(turns<0 || turns>3)return AGError(2,@"旋转参数无效");if(!turns)out=src.clone();else cv::rotate(src,out,turns==1?cv::ROTATE_90_CLOCKWISE:turns==2?cv::ROTATE_180:cv::ROTATE_90_COUNTERCLOCKWISE);
    } else if([op isEqualToString:@"opencv.threshold"]) {
        double value=[args[@"value"] doubleValue];if(!(value>=0 && value<=255))return AGError(2,@"阈值无效");cv::threshold(asGray(src),out,value,255,[args[@"inverse"] boolValue]?cv::THRESH_BINARY_INV:cv::THRESH_BINARY);
    } else if([op isEqualToString:@"opencv.blur"]) {
        int kernel=[args[@"kernel"] intValue];double sigma=[args[@"sigma"] doubleValue];if(kernel<1 || kernel>99 || kernel%2==0 || !(sigma>=0 && sigma<=1000))return AGError(2,@"模糊参数无效");cv::GaussianBlur(src,out,cv::Size(kernel,kernel),sigma);
    } else if([op isEqualToString:@"opencv.canny"]) {
        double low=[args[@"low"] doubleValue],high=[args[@"high"] doubleValue];if(!(low>=0 && high>=low && high<=65535))return AGError(2,@"边缘检测阈值无效");cv::Canny(asGray(src),out,low,high);
    } else return AGError(1,@"没有此 OpenCV 操作");
    return retainMat(out);
 } catch(const cv::Exception &error) { return AGError(3,[NSString stringWithUTF8String:error.what()]); }
 catch(const std::exception &error) { return AGError(3,[NSString stringWithUTF8String:error.what()]); }
#else
 return AGError(1,@"当前制品未链接可用的 OpenCV 真机运行库；不会以空 Mat 冒充成功");
#endif
}
