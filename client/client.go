package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"

	pb "testgrpc/fileserver"

	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
)

const (
	address           = "localhost:50051"
	defaultName       = "world"
	timestampFormat   = time.StampNano
	Size8GB           = 8 * 1024 * 1024 * 1024
	MaxUploadFileSize = Size8GB
	Size4MB           = 4 * 1024 * 1024
)

var kacp = keepalive.ClientParameters{
	Time:                10 * time.Second, // send pings every 10 seconds if there is no activity
	Timeout:             time.Second,      // wait 1 second for ping ack before considering the connection dead
	PermitWithoutStream: true,             // send pings even without active streams
}

func sayHello(c pb.GreeterClient, name string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	r, err := c.SayHello(ctx, &pb.HelloRequest{Name: name})
	if err != nil {
		log.Printf("could not greet: %v", err)
	}
	log.Printf("Greeting: %s", r.Message)
}

func list(c pb.GreeterClient) []*pb.FileInfo {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	r, err := c.List(ctx, &pb.Empty{})
	if err != nil {
		log.Printf("could not greet: %v", err)
	}
	return r.Files
}

func upload(c pb.GreeterClient, filename string) {
	log.Println("starting upload")
	stats, err := os.Stat(filename)
	if err != nil {
		log.Printf("could not greet: %v", err)
	}
	md := metadata.Pairs("timestamp", time.Now().Format(timestampFormat),
		"filename", filepath.Base(filename),
		"size", strconv.Itoa(int(stats.Size())))
	fmt.Println("size: ", stats.Size())
	ctx := metadata.NewOutgoingContext(context.Background(), md)

	stream, err := c.Upload(ctx)
	if err != nil {
		log.Printf("could not greet: %v", err)
	}

	file, err := os.Open(filename)
	if err != nil {
		log.Printf("could not greet: %v", err)
	}
	defer file.Close()
	buf := make([]byte, Size4MB)
	for {
		// put as many bytes as `chunkSize` into the buf array.
		n, err := file.Read(buf)
		if err != nil {
			if err == io.EOF {
				break
			}
			log.Printf("could not greet: %v", err)
			return
		}

		err = stream.Send(&pb.Chunk{
			// because we might've read less than
			// `chunkSize` we want to only send up to
			// `n` (amount of bytes read).
			// note: slicing (`:n`) won't copy the
			// underlying data, so this as fast as taking
			// a "pointer" to the underlying storage.
			Content: buf[:n],
		})
		if err != nil {
			log.Printf("could not greet: %v", err)
			return
		}
	}
	status, err := stream.CloseAndRecv()
	if err != nil {
		log.Printf("could not greet: %v, %v", status, err)
		return
	}
	fmt.Println(status)
}

func download(c pb.GreeterClient, name string) {
	log.Println("starting download")
	stream, err := c.Download(context.Background(), &pb.FileName{Name: name})
	if err != nil {
		log.Fatalf("failed to call ServerStreamingEcho: %v", err)
	}
	defer stream.CloseSend()

	// Read the header when the header arrives.
	header, err := stream.Header()
	if err != nil {
		log.Fatalf("failed to get header from stream: %v", err)
	}
	filename := header["filename"][0]
	filesize, _ := strconv.Atoi(header["size"][0])

	// while there are messages coming
	size := 0
	file, err := os.OpenFile("./"+filename, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()
	for {
		data, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				break
			}
			log.Fatal(err)
		}
		size += len(data.Content)
		file.Write(data.Content)
	}
	if size != filesize {
		log.Fatal("recv size not matched data size")
	}
}

func main() {
	// Set up a connection to the server.
	conn, err := grpc.Dial(address, grpc.WithInsecure(), grpc.WithKeepaliveParams(kacp),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(MaxUploadFileSize)))
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}
	defer conn.Close()
	c := pb.NewGreeterClient(conn)

	sayHello(c, defaultName)

	files := list(c)
	log.Println("files:")
	for _, file := range files {
		fmt.Printf("isdir: %v\tsize: %d\tname: %s\n", file.IsDir, file.Size, file.Name)
	}
	if len(os.Args) >= 2 {
		upload(c, os.Args[1])
	}
	files = list(c)
	log.Println("files:")
	for _, file := range files {
		fmt.Printf("dir: %v\tsize: %d\tname: %s\n", file.IsDir, file.Size, file.Name)
	}
	if len(files) > 0 && !files[0].IsDir {
		download(c, files[0].Name)
	}
}
