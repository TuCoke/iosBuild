//
// Bridging.h — Swift 与 Go c-archive 之间的桥接头。
//
// libagovios.h 由 `go build -buildmode=c-archive` 自动生成在 .a 旁边，
// 这里直接包含它，避免手抄签名导致不一致。
//
#ifndef AGOV_HOST_BRIDGING_H
#define AGOV_HOST_BRIDGING_H

#include "libagovios.h"

#endif
