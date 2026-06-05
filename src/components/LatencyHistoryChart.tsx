import { useEffect, useRef } from "react";

import type { CheckHistoryPoint } from "../types";
import { historyStatusConfig } from "../constants";

export function LatencyHistoryChart({ points }: { points: CheckHistoryPoint[] }) {
  const canvasRef = useRef<HTMLCanvasElement>(null);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const ctx = canvas.getContext("2d");
    if (!ctx) return;

    const dpr = window.devicePixelRatio || 1;
    const width = canvas.offsetWidth;
    const height = canvas.offsetHeight;
    canvas.width = width * dpr;
    canvas.height = height * dpr;
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.clearRect(0, 0, width, height);

    const hasValidValues = points.some((point) => point.latency_ms != null);
    const padding = { top: 14, right: 16, bottom: 24, left: hasValidValues ? 42 : 16 };
    const chartW = width - padding.left - padding.right;
    const chartH = height - padding.top - padding.bottom;
    const values = points.map((point) => point.latency_ms).filter((value): value is number => value != null);
    const min = values.length ? Math.min(...values) : 0;
    const max = values.length ? Math.max(...values) : 1;
    const range = Math.max(1, max - min);

    if (hasValidValues) {
      ctx.strokeStyle = "rgba(255,255,255,0.04)";
      ctx.lineWidth = 1;
      for (let i = 0; i <= 4; i++) {
        const y = padding.top + (chartH / 4) * i;
        ctx.beginPath();
        ctx.moveTo(padding.left, y);
        ctx.lineTo(padding.left + chartW, y);
        ctx.stroke();
      }

      ctx.fillStyle = "rgba(255,255,255,0.18)";
      ctx.font = "10px monospace";
      ctx.textAlign = "right";
      for (let i = 0; i <= 4; i++) {
        const value = Math.round(max - (range / 4) * i);
        ctx.fillText(`${value}ms`, padding.left - 8, padding.top + (chartH / 4) * i + 3);
      }
    }

    ctx.fillStyle = "rgba(255,255,255,0.18)";
    ctx.font = "10px monospace";
    ctx.textAlign = "center";
    const start = new Date(points[0]?.checked_at ?? Date.now());
    const end = new Date(points.at(-1)?.checked_at ?? Date.now());
    const labels = [start, new Date((start.getTime() + end.getTime()) / 2), end];
    labels.forEach((date, index) => {
      const x = padding.left + (chartW / 2) * index;
      ctx.fillText(date.toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit", hour12: false }), x, height - 7);
    });

    const pointX = (index: number) => padding.left + (points.length === 1 ? chartW / 2 : (index / (points.length - 1)) * chartW);
    const failureY = padding.top + chartH + 9;
    const statusBands = points.map((point, index) => {
      const x = pointX(index);
      return {
        x,
        status: point.status,
        latency: point.latency_ms,
        color: historyStatusConfig[point.status].color,
      };
    });

    statusBands.forEach((point, index) => {
      if (point.status === "online" && point.latency != null) return;
      const previousX = index === 0 ? padding.left : pointX(index - 1);
      const nextX = index === points.length - 1 ? padding.left + chartW : pointX(index + 1);
      const left = index === 0 ? point.x : point.x - (point.x - previousX) / 2;
      const right = index === points.length - 1 ? point.x : point.x + (nextX - point.x) / 2;
      ctx.fillStyle = point.status === "timeout" ? "rgba(251,191,36,0.10)" : "rgba(248,113,113,0.10)";
      ctx.fillRect(left, padding.top, Math.max(2, right - left), chartH);
    });

    ctx.fillStyle = "rgba(255,255,255,0.10)";
    ctx.fillRect(padding.left, failureY, chartW, 1);

    const validPoints = points
      .map((point, index) => {
        if (point.latency_ms == null) return null;
        const x = pointX(index);
        const y = padding.top + chartH - ((point.latency_ms - min) / range) * chartH;
        return { x, y, latency: point.latency_ms };
      })
      .filter((point): point is { x: number; y: number; latency: number } => point != null);

    const successSegments: { x: number; y: number; latency: number }[][] = [];
    let currentSegment: { x: number; y: number; latency: number }[] = [];
    points.forEach((point, index) => {
      if (point.latency_ms == null) {
        if (currentSegment.length > 0) {
          successSegments.push(currentSegment);
          currentSegment = [];
        }
        return;
      }
      const x = pointX(index);
      const y = padding.top + chartH - ((point.latency_ms - min) / range) * chartH;
      currentSegment.push({ x, y, latency: point.latency_ms });
    });
    if (currentSegment.length > 0) {
      successSegments.push(currentSegment);
    }

    statusBands.forEach((point) => {
      if (point.status === "online" && point.latency != null) return;
      ctx.beginPath();
      ctx.arc(point.x, failureY, 3.5, 0, Math.PI * 2);
      ctx.fillStyle = point.color;
      ctx.fill();
    });

    if (!validPoints.length) {
      ctx.fillStyle = "rgba(255,255,255,0.22)";
      ctx.textAlign = "center";
      ctx.fillText("近 1 小时没有成功延迟点，底部标记为失败记录", width / 2, height / 2);
      drawHistoryLegend(ctx, width, padding.top, [
        { color: historyStatusConfig.timeout.color, label: "超时" },
        { color: historyStatusConfig.offline.color, label: "离线" },
      ]);
      return;
    }

    const gradient = ctx.createLinearGradient(0, padding.top, 0, padding.top + chartH);
    gradient.addColorStop(0, "rgba(56, 189, 248, 0.20)");
    gradient.addColorStop(1, "rgba(56, 189, 248, 0.00)");

    successSegments.forEach((segment) => {
      ctx.beginPath();
      ctx.moveTo(segment[0].x, padding.top + chartH);
      segment.forEach((point) => ctx.lineTo(point.x, point.y));
      ctx.lineTo(segment.at(-1)!.x, padding.top + chartH);
      ctx.closePath();
      ctx.fillStyle = gradient;
      ctx.fill();

      ctx.beginPath();
      ctx.strokeStyle = "#38bdf8";
      ctx.lineWidth = 1.6;
      ctx.lineCap = "round";
      ctx.lineJoin = "round";
      segment.forEach((point, index) => {
        if (index === 0) ctx.moveTo(point.x, point.y);
        else ctx.lineTo(point.x, point.y);
      });
      ctx.stroke();
    });

    validPoints.forEach((point) => {
      ctx.beginPath();
      ctx.arc(point.x, point.y, 2.2, 0, Math.PI * 2);
      ctx.fillStyle =
        point.latency < 100 ? "#34d399"
        : point.latency < 200 ? "#fbbf24"
        : "#f87171";
      ctx.fill();
    });

    drawHistoryLegend(ctx, width, padding.top, [
      { color: "#38bdf8", label: "延迟" },
      { color: historyStatusConfig.timeout.color, label: "超时" },
      { color: historyStatusConfig.offline.color, label: "离线" },
    ]);
  }, [points]);

  return <canvas ref={canvasRef} className="w-full" style={{ height: "180px" }} />;
}

function drawHistoryLegend(
  ctx: CanvasRenderingContext2D,
  width: number,
  top: number,
  items: { color: string; label: string }[],
) {
  ctx.font = "10px monospace";
  ctx.textAlign = "left";
  ctx.textBaseline = "middle";
  let x = Math.max(52, width - 150);
  const y = top + 4;
  items.forEach((item) => {
    ctx.beginPath();
    ctx.arc(x, y, 3, 0, Math.PI * 2);
    ctx.fillStyle = item.color;
    ctx.fill();
    ctx.fillStyle = "rgba(255,255,255,0.30)";
    ctx.fillText(item.label, x + 7, y);
    x += item.label.length * 10 + 22;
  });
  ctx.textBaseline = "alphabetic";
}
