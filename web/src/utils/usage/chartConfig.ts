/**
 * Chart.js configuration utilities for usage statistics
 * Extracted from UsagePage.tsx for reusability
 */

import type { ChartOptions } from 'chart.js';

/**
 * Static sparkline chart options (no dependencies on theme/mobile)
 */
export const sparklineOptions: ChartOptions<'line'> = {
  responsive: true,
  maintainAspectRatio: false,
  plugins: { legend: { display: false }, tooltip: { enabled: false } },
  scales: { x: { display: false }, y: { display: false } },
  elements: { line: { tension: 0.45 }, point: { radius: 0 } }
};

export interface ChartConfigOptions {
  period: 'hour' | 'day';
  labels: string[];
  isDark: boolean;
  isMobile: boolean;
}

/**
 * Build chart options with theme and responsive awareness
 */
// Neo-Brutalism Chart Config
export function buildChartOptions({
  period,
  labels,
  isDark,
  isMobile
}: ChartConfigOptions): ChartOptions<'line'> {
  const pointRadius = isMobile && period === 'hour' ? 0 : isMobile ? 3 : 5;
  const tickFontSize = isMobile ? 11 : 13;
  const maxTickLabelCount = isMobile ? (period === 'hour' ? 8 : 6) : period === 'hour' ? 12 : 10;
  
  // High contrast colors
  const gridColor = isDark ? 'rgba(255, 255, 255, 0.2)' : 'rgba(0, 0, 0, 0.2)'; 
  const axisBorderColor = isDark ? '#ffffff' : '#000000';
  const tickColor = isDark ? '#ffffff' : '#000000';
  
  // Brutal Tooltip
  const tooltipBg = isDark ? '#000000' : '#ffffff';
  const tooltipTitle = isDark ? '#ffffff' : '#000000';
  const tooltipBody = isDark ? '#ffffff' : '#000000';
  const tooltipBorder = isDark ? '#ffffff' : '#000000';

  return {
    responsive: true,
    maintainAspectRatio: false,
    interaction: {
      mode: 'index',
      intersect: false
    },
    plugins: {
      legend: { display: false },
      tooltip: {
        backgroundColor: tooltipBg,
        titleColor: tooltipTitle,
        bodyColor: tooltipBody,
        borderColor: tooltipBorder,
        borderWidth: 2,
        padding: 12,
        displayColors: true,
        usePointStyle: true,
        titleFont: {
            weight: 'bold',
            size: 14
        },
        bodyFont: {
            weight: 'bold'
        },
        // Hard sharp edges for tooltip
        cornerRadius: 0,
        caretSize: 0, // No caret
      }
    },
    scales: {
      x: {
        grid: {
          color: gridColor,
          drawTicks: false,
          lineWidth: 2 // Thicker grid
        },
        border: {
          color: axisBorderColor,
          width: 3 // Thicker axis
        },
        ticks: {
          color: tickColor,
          font: { size: tickFontSize, weight: 'bold', family: 'monospace' }, // Monospace works well
          maxRotation: isMobile ? 0 : 45,
          minRotation: 0,
          autoSkip: true,
          maxTicksLimit: maxTickLabelCount,
          callback: (value) => {
            const index = typeof value === 'number' ? value : Number(value);
            const raw =
              Number.isFinite(index) && labels[index] ? labels[index] : typeof value === 'string' ? value : '';

            if (period === 'hour') {
              const [md, time] = raw.split(' ');
              if (!time) return raw;
              if (time.startsWith('00:')) {
                return md ? [md, time] : time;
              }
              return time;
            }

            if (isMobile) {
              const parts = raw.split('-');
              if (parts.length === 3) {
                return `${parts[1]}-${parts[2]}`;
              }
            }
            return raw;
          }
        }
      },
      y: {
        beginAtZero: true,
        grid: {
          color: gridColor,
          lineWidth: 2
        },
        border: {
          color: axisBorderColor,
          width: 3
        },
        ticks: {
          color: tickColor,
          font: { size: tickFontSize, weight: 'bold', family: 'monospace' }
        }
      }
    },
    elements: {
      line: {
        tension: 0, // Zero tension for straight lines (part of the look) OR keep slight curve but thick. Let's try 0 first for "hard" look.
        borderWidth: isMobile ? 2 : 4
      },
      point: {
        borderWidth: 2,
        radius: pointRadius,
        hoverRadius: pointRadius + 2,
        borderColor: axisBorderColor, // Black border around points
      }
    }
  };
}

/**
 * Calculate minimum chart width for hourly data on mobile devices
 */
export function getHourChartMinWidth(labelCount: number, isMobile: boolean): string | undefined {
  if (!isMobile || labelCount <= 0) return undefined;
  const perPoint = 56;
  const minWidth = Math.min(labelCount * perPoint, 3000);
  return `${minWidth}px`;
}
