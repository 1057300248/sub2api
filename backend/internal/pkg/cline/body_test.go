package cline

import (
 "errors"
 "io"
 "strings"
 "testing"
)
func TestClineSSEFailurePreventsSuccessfulTerminal(t *testing.T){
 input:="data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\ndata: {\"choices\":[{\"finish_reason\":\"error\"}]}\n\ndata: [DONE]\n\n"
 calls:=0;r:=GuardSSEBody(io.NopCloser(strings.NewReader(input)),1024,func(body []byte){calls++});defer r.Close()
 output,err:=io.ReadAll(r);if !errors.Is(err,ErrStreamFailure)||calls!=1||!strings.Contains(string(output),"partial")||strings.Contains(string(output),"[DONE]"){t.Fatalf("output=%s error=%v callbacks=%d",output,err,calls)}
}
func TestClineSSEPassesNormalDataAndBoundsMemory(t *testing.T){
 input:="data: {\"choices\":[{\"delta\":{\"content\":\"error is normal text\"}}]}\n\ndata: [DONE]\n\n"
 r:=GuardSSEBody(io.NopCloser(strings.NewReader(input)),1024,nil);output,err:=io.ReadAll(r);if err!=nil||string(output)!=input{t.Fatalf("%s %v",output,err)}
 r=GuardSSEBody(io.NopCloser(strings.NewReader(strings.Repeat("x",100))),32,nil);if _,err=io.ReadAll(r);err==nil{t.Fatal("unbounded line accepted")}
}
