import { useCallback, useEffect, useRef } from "react";
import type { Position } from "css-box-model";
import type {
  FluidDragActions,
  PreDragActions,
  SensorAPI,
} from "@hello-pangea/dnd";

const LONG_PRESS_MS = 120;
const MOVE_THRESHOLD_PX = 8;

type Phase =
  | { type: "IDLE" }
  | {
      type: "PENDING";
      origin: Position;
      actions: PreDragActions;
      longPressTimer: number;
    }
  | { type: "DRAGGING"; actions: FluidDragActions };

const IDLE: Phase = { type: "IDLE" };

export default function useTouchSensor(api: SensorAPI) {
  const phaseRef = useRef<Phase>(IDLE);
  const unbindActiveRef = useRef<() => void>(() => {});

  const stop = useCallback(() => {
    const phase = phaseRef.current;
    if (phase.type === "PENDING") {
      window.clearTimeout(phase.longPressTimer);
    }
    phaseRef.current = IDLE;
    unbindActiveRef.current();
    unbindActiveRef.current = () => {};
  }, []);

  const cancel = useCallback(() => {
    const phase = phaseRef.current;
    stop();
    if (phase.type === "DRAGGING") {
      phase.actions.cancel({ shouldBlockNextClick: true });
    } else if (phase.type === "PENDING") {
      phase.actions.abort();
    }
  }, [stop]);

  useEffect(() => {
    const liftToDragging = (point: Position) => {
      const phase = phaseRef.current;
      if (phase.type !== "PENDING") return;
      window.clearTimeout(phase.longPressTimer);
      const dragActions = phase.actions.fluidLift(point);
      phaseRef.current = { type: "DRAGGING", actions: dragActions };
    };

    const onTouchMove = (event: TouchEvent) => {
      const phase = phaseRef.current;
      const touch = event.touches[0];
      if (!touch) return;
      const point: Position = { x: touch.clientX, y: touch.clientY };

      if (phase.type === "PENDING") {
        const dx = point.x - phase.origin.x;
        const dy = point.y - phase.origin.y;
        if (Math.hypot(dx, dy) >= MOVE_THRESHOLD_PX) {
          liftToDragging(point);
          event.preventDefault();
        }
        return;
      }
      if (phase.type === "DRAGGING") {
        event.preventDefault();
        phase.actions.move(point);
      }
    };

    const onTouchEnd = (event: TouchEvent) => {
      const phase = phaseRef.current;
      if (phase.type === "DRAGGING") {
        event.preventDefault();
        phase.actions.drop({ shouldBlockNextClick: true });
        stop();
      } else if (phase.type === "PENDING") {
        phase.actions.abort();
        stop();
      }
    };

    const onCancel = () => cancel();

    const bindActive = () => {
      const opts: AddEventListenerOptions = { capture: true, passive: false };
      window.addEventListener("touchmove", onTouchMove, opts);
      window.addEventListener("touchend", onTouchEnd, opts);
      window.addEventListener("touchcancel", onCancel, opts);
      window.addEventListener("orientationchange", onCancel, opts);
      window.addEventListener("resize", onCancel, opts);
      document.addEventListener("visibilitychange", onCancel, opts);
      unbindActiveRef.current = () => {
        window.removeEventListener("touchmove", onTouchMove, opts);
        window.removeEventListener("touchend", onTouchEnd, opts);
        window.removeEventListener("touchcancel", onCancel, opts);
        window.removeEventListener("orientationchange", onCancel, opts);
        window.removeEventListener("resize", onCancel, opts);
        document.removeEventListener("visibilitychange", onCancel, opts);
      };
    };

    const onTouchStart = (event: TouchEvent) => {
      if (event.defaultPrevented) return;
      if (phaseRef.current.type !== "IDLE") return;

      const draggableId = api.findClosestDraggableId(event);
      if (!draggableId) return;

      const actions = api.tryGetLock(draggableId, stop, { sourceEvent: event });
      if (!actions) return;

      const touch = event.touches[0];
      const origin: Position = { x: touch.clientX, y: touch.clientY };

      const longPressTimer = window.setTimeout(() => {
        liftToDragging(origin);
      }, LONG_PRESS_MS);

      phaseRef.current = {
        type: "PENDING",
        origin,
        actions,
        longPressTimer,
      };

      bindActive();
    };

    const opts: AddEventListenerOptions = { capture: true, passive: false };
    window.addEventListener("touchstart", onTouchStart, opts);
    return () => {
      window.removeEventListener("touchstart", onTouchStart, opts);
      const phase = phaseRef.current;
      if (phase.type === "PENDING") {
        window.clearTimeout(phase.longPressTimer);
        phaseRef.current = IDLE;
      }
      unbindActiveRef.current();
    };
  }, [api, cancel, stop]);

  // Safari needs a non-passive touchmove handler somewhere so that
  // preventDefault() on dynamically attached touchmove handlers actually works.
  // https://github.com/atlassian/react-beautiful-dnd/issues/1374
  useEffect(() => {
    const noop = () => {};
    const opts: AddEventListenerOptions = { capture: false, passive: false };
    window.addEventListener("touchmove", noop, opts);
    return () => window.removeEventListener("touchmove", noop, opts);
  }, []);
}
