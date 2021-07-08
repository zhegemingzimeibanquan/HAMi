/*
 * Copyright © 2021 peizhaoyou <peizhaoyou@4paradigm.com>
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package device_plugin

import (
    "context"
    "fmt"
    "k8s.io/klog/v2"
    "time"

    pb "4pd.io/k8s-vgpu/pkg/api"
    "4pd.io/k8s-vgpu/pkg/device-plugin/config"
    "google.golang.org/grpc"
)

type DeviceRegister struct {
    changed  chan struct{}
    stop     chan struct{}
    resource ResourceManager
}

func NewDeviceRegister(changed chan struct{}, resourceManager ResourceManager) *DeviceRegister {
    return &DeviceRegister{
        changed:  changed,
        stop:     make(chan struct{}),
        resource: resourceManager,
    }
}

func (r *DeviceRegister) Start() {
    go r.WatchAndRegister()
}

func (r *DeviceRegister) Stop() {
    close(r.stop)
}

func (r *DeviceRegister) apiDevices() *[]*pb.DeviceInfo {
    devs := r.resource.Devices()
    res := make([]*pb.DeviceInfo, 0, len(devs))
    for _, dev := range devs {
        res = append(res, &pb.DeviceInfo{
            Id:     dev.ID,
            Count:  int32(config.DeviceSplitCount),
            Health: dev.Health == "healthy",
        })
    }
    return &res
}

func (r *DeviceRegister) Register(ctx context.Context) error {
    conn, err := grpc.DialContext(
        ctx,
        config.SchedulerEndpoint,
        grpc.WithInsecure(),
        grpc.WithBlock(),
        //grpc.WithConnectParams(grpc.ConnectParams{MinConnectTimeout: 3}),
    )
    if err != nil {
        return fmt.Errorf("connect scheduler error, %v", err)
    }
    client := pb.NewDeviceServiceClient(conn)
    register, err := client.Register(ctx)
    if err != nil {
        klog.Errorf("register error %v", err)
        err = fmt.Errorf("client register error, %v", err)
        return err
    }
    req := pb.RegisterRequest{Node: config.NodeName, Devices: *r.apiDevices()}
    err = register.Send(&req)
    if err != nil {
        klog.Errorf("register send error, %v", err)
        return err
    }
    klog.V(3).Infof("register info %v", req.String())
    closeCh := make(chan struct{})
    go func() {
        reply := pb.RegisterReply{}
        err := register.RecvMsg(reply)
        if err != nil {
            klog.Errorf("register recv error, %v", err)
        } else {
            klog.Errorf("register recv closed")
        }
        closeCh <- struct{}{}
    }()
    for {
        select {
        case <-r.changed:
            err = register.Send(&pb.RegisterRequest{
                Node:    config.NodeName,
                Devices: *r.apiDevices(),
            })
            if err != nil {
                klog.Errorf("register send error, %v", err)
                return err
            }
            klog.V(3).Infof("register info %v", req.String())
        case <-closeCh:
            klog.Infof("register server closed")
            return fmt.Errorf("register server closed")
        case <-r.stop:
            klog.Infof("register stoped")
            return nil
        }
    }
}

func (r *DeviceRegister) WatchAndRegister() {
    //ctx, cancel := context.WithTimeout(context.Background(), time.Second*30)
    //defer cancel()
    ctx := context.Background()
    for {
        err := r.Register(ctx)
        if err != nil {
            klog.Errorf("register error, %v", err)
            time.Sleep(time.Second * 5)
        } else {
            klog.Infof("register stopped")
            break
        }
    }
}
