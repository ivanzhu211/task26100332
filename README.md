# reservation

进程内的设施区域可预约状态库（纯内存，无持久化）。

## 模型

- 区域组成森林，无父区域的是设施（`AddFacility`），其下可任意嵌套
  （`AddZone(parent)`）。
- 每件资产恰好属于一个区域（`AddAsset(zone, status)`），状态为
  `StatusNormal` / `StatusMaintenance` / `StatusDisabled`，后两者为阻塞态。
- 区域 Z 可预约，当且仅当：Z 的子树（含自身）无阻塞态资产；Z 及其所有
  祖先无有效预约；Z 的所有后代无有效预约。

## API

```go
m := reservation.NewManager()
fac, _ := m.AddFacility(), error(nil)
bld, _ := m.AddZone(fac)
a, _ := m.AddAsset(bld, reservation.StatusMaintenance)

m.SetAssetStatus(a, reservation.StatusNormal) // 任何时刻允许
m.MoveAsset(a, bld)                           // 任何时刻允许
m.MoveZone(bld, fac)                          // 成环或预约嵌套冲突时拒绝
id, err := m.Reserve(bld)                     // 不满足定义时返回 ErrNotReservable
m.Release(id)
ok := m.IsReservable(bld)
```

## 并发设计

- 每个区域一个 `atomic.Int64` 状态字，打包三个计数：子树阻塞资产数
  （bit 0..29）、子树内有效预约数（bit 30..59）、自身预约位（bit 60）。
  `Reserve` 对自身的认领是单条 `CAS(0, selfUnit)`，对祖先的
  “检查无自约 + 子树预约数 +1”是逐节点 CAS 循环，冲突即回滚拒绝。
- `SetAssetStatus` / `MoveAsset` / `Reserve` / `IsReservable` 沿父链
  自顶向下加**读锁**（共享锁，互不为祖先后代的操作在共同祖先上从不
  互相等待），加锁后重新校验路径，被并发 `MoveZone` 打乱则重试。
  开销均为 O(深度)。
- `MoveZone` 由一把 `moveMu` 串行化，对新旧两条祖先链按 (深度, ID)
  全局序加**写锁**（与读锁同序，无死锁），然后把被移动子树的聚合计数
  整体从旧链搬到新链，开销 O(深度)，与子树规模无关。
- `MoveAsset` / `MoveZone` 搬迁计数时先加新链、再减旧链，共同祖先
  （如同一栋楼）的阻塞计数全程 ≥1，绝不出现一瞬间可预约。
- 每件资产一把小锁，串行化该资产上的状态变更与搬动，保证
  (status, zone) 与计数传播一致。

## 测试

```sh
go test -race ./...
```

覆盖：楼内搬设备 + 跨层挂房间期间楼不可约；多层树随机混跑全部操作
（含并发成环 MoveZone 对）后从头重算比对；同一祖先链上并发
Reserve/Release 的排他不变式。
