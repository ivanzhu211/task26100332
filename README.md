# task26100332

进程内设施区域可预约状态库（package `facility`），全部状态在内存中。

## 模型

- 区域组成森林，无父区域的是设施根，任意嵌套，深度一般 ≤ 8。
- 资产恰好属于一个区域，状态为 `Normal` / `Maintenance` / `Decommissioned`，后两者为阻塞态。
- 区域 Z 可预约 ⟺ Z 的子树无阻塞态资产，且 Z 自身、所有祖先、所有后代都无有效预约。

## API

`AddZone` / `AddAsset` / `SetAssetStatus` / `MoveAsset` / `MoveZone` /
`Reserve` / `Release` / `IsReservable`，见各文件 doc comment。

## 并发设计

- 每个区域三个原子量：`state`（空闲/待定/已确认）、`subRes`（子树有效预约数）、
  `subBlock`（子树阻塞资产数）。普通操作沿父链用原子指令传播计数，
  不在任何锁上等待；互不为祖先后代的操作在共同祖先上只有原子指令级别的竞争。
- 一把 `sync.RWMutex`：所有普通操作持读锁（互不阻塞），`MoveZone` 持写锁
  （MoveZone 之间串行，执行期间其他操作等待——需求允许）。因此普通操作期间
  父链结构稳定，MoveZone 的计数迁移无人观察中间态。
- `Reserve` 与祖先/后代上的并发 `Reserve` 用 Dekker 式配对：
  祖先方 `CAS(state)` 后复读 `subRes`，后代方沿链 `subRes+1` 后读祖先 `state`；
  顺序一致原子保证至少一方看到对方，两个祖孙关系的预约不可能同时确认。
- 计数永不短暂低估：`SetAssetStatus` 变阻塞先传播 +1 再提交状态，恢复正常先提交再传播 -1；
  `MoveAsset`/`MoveZone` 先在新链 +1 再在老链 -1，搬移过程中的楼/房间计数不会瞬时归零。
  同一资产的计数迁移在该资产锁内完成，贡献不会为负、不会丢失。
- 所有操作 O(深度)，MoveZone 与被移动子树规模无关；无后台重算。

## 测试

`go test -race ./...`（本机沙盒需 `GOCACHE=/tmp/gocache`）。

- `TestBuildingNeverReservable`：维修中的设备在楼内搬动、所在房间在楼层间挂接，
  并发对楼 `Reserve`/`IsReservable`，成功一次即失败。
- `TestRandomConcurrentOps`：多层树上随机混跑全部操作，含一对并发成环 `MoveZone`；
  停下后从头重算每个区域的可预约性、计数与树结构逐一比对。
- `TestChainReserveRelease`：同一祖先链上并发 `Reserve`/`Release`，
  任何时刻不存在两个祖孙关系的有效预约（worker 持有自己的预约期间检查他 zone，判定为真并发）。
