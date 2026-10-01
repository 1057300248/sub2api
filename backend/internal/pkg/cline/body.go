package cline

import (
 "bufio"
 "bytes"
 "fmt"
 "io"
)

// GuardSSEBody turns explicit generation-error events into read failures. The
// shared Chat/Responses/Messages readers then skip successful finalization. It
// never scans generated prose for error-like words and does not replay requests.
func GuardSSEBody(body io.ReadCloser,maxLine int,onError func([]byte))io.ReadCloser{
 if maxLine<=0{maxLine=1<<20}
 return &guardedSSEBody{source:body,reader:bufio.NewReaderSize(body,16<<10),maxLine:maxLine,onError:onError}
}
type guardedSSEBody struct{source io.ReadCloser;reader *bufio.Reader;pending []byte;terminal error;maxLine int;onError func([]byte)}
func (b *guardedSSEBody) Close()error{return b.source.Close()}
func (b *guardedSSEBody) Read(p []byte)(int,error){
 if len(p)==0{return 0,nil}
 if len(b.pending)>0{n:=copy(p,b.pending);b.pending=b.pending[n:];return n,nil}
 if b.terminal!=nil{return 0,b.terminal}
 var line []byte
 for {
  part,err:=b.reader.ReadSlice('\n')
  if len(line)+len(part)>b.maxLine{b.terminal=fmt.Errorf("Cline SSE line exceeds configured limit");return 0,b.terminal}
  line=append(line,part...)
  if err==bufio.ErrBufferFull{continue}
  if err!=nil{b.terminal=err}
  break
 }
 trimmed:=bytes.TrimSpace(line)
 if bytes.HasPrefix(trimmed,[]byte("data:")){
  payload:=bytes.TrimSpace(trimmed[len("data:"):])
  if HasGenerationError(payload){b.terminal=ErrStreamFailure;if b.onError!=nil{b.onError(payload)};return 0,b.terminal}
 }
 if len(line)==0{return 0,b.terminal}
 n:=copy(p,line);b.pending=line[n:];return n,nil
}
