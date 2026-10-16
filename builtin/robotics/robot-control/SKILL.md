---
name: robot-control
description: Safely inspect, stop, and move configured OpenKitty robots through constrained robot tools. Use for natural-language or voice requests involving an unmanned rover, mobile robot, Raspberry Pi robot, robot body, motor movement, forward/backward travel, turning, emergency stop, or checking robot state. 中文触发包括机器人、机器人小车、移动小车、前进、后退、左转、右转、转向、停止和状态检查。
tags:
  - robot
  - robotics
  - mobile rover
  - 机器人
  - 机器人小车
  - 小车运动控制
read_when:
  - 用户要求控制机器人或小车移动、转向、停止或查看状态
  - the user asks to move, turn, stop, or inspect a robot
---

# Robot Control

Use the deterministic robot runtime between natural language and hardware.
Never bypass it with shell, arbitrary serial bytes, ROS topics, GPIO writes, or
raw Modbus registers.

## Safe workflow

1. Call `robot_status` before motion. Confirm `connected=true`,
   `motion_enabled=true`, no safety fault, and no active command.
2. For one short text or ASR instruction with an explicit unit, prefer
   `robot_command`. It deterministically parses commands such as “前进20厘米”,
   “右转90度”, and “stop”.
3. Otherwise convert exactly one explicit user instruction into one bounded
   action:
   - forward/backward: call `robot_move` with `motion=translate` and signed
     `distance_m`;
   - left/right turn: call `robot_move` with `motion=rotate` and signed
     `angle_deg`.
4. Use SI units. Convert centimetres to metres. Positive distance is forward;
   negative is backward. Positive angle is left/counter-clockwise; negative is
   right/clockwise.
5. Supply a stable unique `command_id` when retrying may occur. Never reuse an
   ID for different parameters.
6. Report the returned command ID and accepted duration. Call `robot_status`
   again to verify completion when the user needs confirmation.

## Stop path

Call `robot_stop` immediately for any stop intent, new obstacle report, lost
control, contradictory instruction, uncertain state, or driver error. Do not
wait for another status call or ask for confirmation. A software stop does not
replace the physical emergency-stop circuit.

## Boundaries

- Do not invent a device name. Omit it only when a configured default exists.
- Do not exceed runtime limits or split one rejected motion into multiple calls
  to evade a distance, speed, turn, timeout, or watchdog limit.
- Do not issue continuous unattended velocity commands.
- Do not claim physical movement while the driver is `sim`.
- Navigation, arbitrary coordinates, arm joints, grippers, and visual grasping
  are not implemented by these three tools.
