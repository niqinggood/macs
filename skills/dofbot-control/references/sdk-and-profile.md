# Dofbot SDK and profile reference

## SDK mapping

The bridge calls only these fixed functions from the installed `Arm_Lib`
package:

| Bridge action | SDK call |
|---|---|
| `status` | `Arm_serial_servo_read(id)` for IDs 1–6 |
| `move-joint` | `Arm_serial_servo_write(id, angle, time)` |
| `move-pose` | `Arm_serial_servo_write6(s1, s2, s3, s4, s5, s6, time)` |
| `gripper` | `Arm_serial_servo_write(6, angle, time)` |
| `stop` | `Arm_Action_Mode(0)` |

The driver defaults to `/dev/myserial` at 115200 baud. `Arm_Device(port)` owns
the serial connection. Joint 5 has a vendor range of 0–270 degrees; joints
1–4 and 6 use 0–180 degrees. Joint 6 is the gripper.

The bridge deliberately excludes arbitrary function names, raw frames,
calibration offsets, servo-ID changes, board reset, stored-action execution,
and torque changes.

## Optional operator profile

Use a profile only when the operator supplies or approves it. Never create or
loosen one merely to make a rejected command pass.

```json
{
  "port": "/dev/myserial",
  "joint_limits_deg": [
    [10, 170],
    [20, 160],
    [20, 160],
    [10, 170],
    [10, 260],
    [30, 150]
  ],
  "max_delta_deg": [10, 10, 10, 10, 15, 25],
  "poses": {
    "operator-approved-home": [90, 90, 90, 90, 90, 90]
  }
}
```

Pass it before the action:

```bash
python3 scripts/dofbot_sdk_py3.py \
  --profile /absolute/operator/profile.json \
  move-pose --name operator-approved-home --duration-ms 1200 --execute
```

A profile may only narrow the vendor angle limits and built-in per-call delta
limits. The bridge rejects profiles that widen them.

## Result semantics

- `physical=false`: no real movement occurred, including doctor, dry-run, and
  mock calls.
- `verified=true`: post-command SDK read-back was within six degrees.
- `motion_state=unknown`: a command may have reached hardware but read-back did
  not establish the result. Do not retry automatically.
- `retry_safe=false`: never repeat the call without a fresh operator decision
  and status check.
- `action_group_stop_sent`: `Arm_Action_Mode(0)` returned; it is not proof that
  an in-flight servo interpolation stopped.

## Troubleshooting

- `sdk_unavailable`: use the Dofbot VM where `Arm_Lib` is installed, or have the
  operator repair the SDK installation.
- `port_exists=false`: pass the CH340 USB device through to the VM and verify
  the `/dev/myserial` udev mapping.
- `open_failed` or `port_busy`: fully close Jupyter, ROS nodes, the Yahboom
  upper-computer process, and other arm programs.
- `read_failed`: do not move. Check power, USB, firmware, servo IDs, and
  calibration with an operator.
- Python 2.7 is supported for legacy systems but is end-of-life. Prefer the
  Python 3 entry whenever the supplied environment supports it.
