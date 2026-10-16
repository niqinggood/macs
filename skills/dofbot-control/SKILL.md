---
name: dofbot-control
description: Safely inspect and operate a Yahboom Dofbot or Dofbot SE robotic arm through the installed Arm_Lib Python SDK. Use for Dofbot connection checks, reading six servo angles, bounded single-joint movement, six-joint poses, gripper control, action-group stop requests, or troubleshooting Python 2.7/Python 3 SDK environments. 中文触发包括 Dofbot、机械臂、六轴、舵机、夹爪、读取角度、姿态控制、Arm_Lib 和机械臂停止。
---

# Dofbot Control

Use the bundled Python bridge instead of composing raw UART frames or arbitrary
Python SDK calls. The bridge exposes only approved `Arm_Lib` functions and
returns one JSON result.

## Select the interpreter

Use the absolute Skill directory returned by `use_skill`; call its scripts
directly and do not search for another copy.

1. Prefer Python 3:
   `python3 "<skill-dir>/scripts/dofbot_sdk_py3.py" doctor`
2. Use Python 2.7 only when the target explicitly requires it:
   `python2.7 "<skill-dir>/scripts/dofbot_sdk_py2.py" doctor`
3. Never install Python, pyserial, or `Arm_Lib` automatically. Report a failed
   doctor check and let the operator repair the supplied Dofbot environment.

## Mandatory workflow

1. Run `doctor`. This imports the SDK and inspects the configured port without
   opening it or moving the arm.
2. Run `status` before every physical movement. Require six valid joint angles.
3. Run the requested movement once. Without an explicit physical-action request
   in the current user turn, omit `--execute`; the bridge returns a dry run.
4. Inspect the JSON receipt. Treat `verified=false` or `motion_state=unknown` as
   uncertain physical state. Never retry automatically.
5. Run `status` after a verified movement when the user needs confirmation.

The default port is `/dev/myserial`. Use `--port` only for an exact
operator-supplied device path.

## Approved commands

```bash
# Read-only environment check
python3 "<skill-dir>/scripts/dofbot_sdk_py3.py" doctor

# Read all six joint angles
python3 "<skill-dir>/scripts/dofbot_sdk_py3.py" status

# Dry run; does not import the SDK or open the serial port
python3 "<skill-dir>/scripts/dofbot_sdk_py3.py" move-joint \
  --joint 1 --angle 95 --duration-ms 800

# Physical movement; only for an explicit current-turn user instruction
python3 "<skill-dir>/scripts/dofbot_sdk_py3.py" move-joint \
  --joint 1 --angle 95 --duration-ms 800 --execute

python3 "<skill-dir>/scripts/dofbot_sdk_py3.py" move-pose \
  --joints 90 90 90 90 90 90 --duration-ms 1200 --execute

python3 "<skill-dir>/scripts/dofbot_sdk_py3.py" gripper \
  --angle 60 --duration-ms 600 --execute

# Best-effort stop for controller action groups
python3 "<skill-dir>/scripts/dofbot_sdk_py3.py" stop
```

The Python 2.7 entry accepts the same arguments.

## Safety boundaries

- Execute only one arm command at a time. Never create movement loops or run
  commands in parallel.
- Never invent joint angles, a port, a named pose, or a profile path.
- Never split a rejected movement into smaller calls to evade a delta limit.
- Never use duration `0`; the bridge enforces 300–5000 ms.
- Never call raw `uart`, `python -c`, `getattr`, SDK maintenance methods,
  servo-ID changes, calibration offsets, board reset, or torque-off.
- `stop` calls `Arm_Action_Mode(0)`. It stops controller action groups but is
  not a proven emergency stop for an in-flight servo interpolation.
- Do not use torque-off as an emergency stop; an unloaded arm can fall.
  In danger, tell the operator to use the physical power cut/emergency stop.
- Shut down Jupyter notebooks, the Yahboom upper-computer application, ROS
  control nodes, and other arm programs before using this Skill.
- A mock result always has `physical=false`; never describe it as real motion.

Read [references/sdk-and-profile.md](references/sdk-and-profile.md) only when
you need the SDK mapping, profile format, result semantics, or troubleshooting.
