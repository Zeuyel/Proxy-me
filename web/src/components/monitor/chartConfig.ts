/**
 * Neobrutalism 风格的图表配置辅助函数
 * 基于 https://www.neobrutalism.dev/charts 的设计原则
 */

export interface NeobrutChartOptions {
  isDark: boolean;
  t: (key: string) => string;
}

/**
 * 获取 Neobrutalism 风格的 Tooltip 配置
 */
export function getNeobrutTooltipConfig(isDark: boolean) {
  return {
    enabled: true,
    backgroundColor: isDark ? '#111111' : '#ffffff',
    titleColor: isDark ? '#ffffff' : '#111111',
    bodyColor: isDark ? '#f5f5f5' : '#111111',
    borderColor: '#111111', // 统一黑色边框
    borderWidth: 3, // 粗边框
    padding: 16,
    cornerRadius: 0, // 方形，无圆角
    displayColors: true,
    boxWidth: 14,
    boxHeight: 14,
    boxPadding: 8,
    titleFont: {
      size: 13,
      weight: 700,
    },
    bodyFont: {
      size: 12,
      weight: 600,
    },
  };
}

/**
 * 获取 Neobrutalism 风格的图例配置
 */
export function getNeobrutLegendConfig(isDark: boolean) {
  return {
    display: true,
    position: 'bottom' as const,
    labels: {
      color: isDark ? '#f5f5f5' : '#111111',
      usePointStyle: true,
      padding: 20,
      font: {
        size: 12,
        weight: 600,
      },
    },
  };
}

/**
 * 获取 Neobrutalism 风格的坐标轴配置
 */
export function getNeobrutAxisConfig(isDark: boolean, options?: {
  stacked?: boolean;
  position?: 'left' | 'right' | 'top' | 'bottom';
  title?: string;
  callback?: (value: string | number) => string;
  drawOnChartArea?: boolean;
}) {
  const {
    stacked = false,
    position = 'left',
    title,
    callback,
    drawOnChartArea = true,
  } = options || {};

  return {
    stacked,
    position,
    grid: {
      color: isDark ? 'rgba(255, 255, 255, 0.15)' : 'rgba(0, 0, 0, 0.15)',
      lineWidth: 2, // 粗网格线
      drawOnChartArea,
    },
    ticks: {
      color: isDark ? '#f5f5f5' : '#111111',
      font: {
        size: 12,
        weight: 600,
      },
      callback,
    },
    border: {
      color: '#111111', // 统一黑色边框
      width: 3, // 粗边框
    },
    ...(title && {
      title: {
        display: true,
        text: title,
        color: isDark ? '#f5f5f5' : '#111111',
        font: {
          size: 12,
          weight: 700,
        },
      },
    }),
  };
}

/**
 * 获取 Neobrutalism 风格的线条数据集配置
 */
export function getNeobrutLineDataset(options: {
  label: string;
  data: number[];
  color: string;
  yAxisID?: string;
  tension?: number;
}) {
  const { label, data, color, yAxisID = 'y', tension = 0 } = options;

  return {
    type: 'line' as const,
    label,
    data,
    borderColor: color,
    backgroundColor: color,
    borderWidth: 4, // 粗线条
    fill: false,
    tension, // 直线无曲线
    yAxisID,
    order: 0,
    pointRadius: 6, // 更大的点
    pointBorderWidth: 3, // 粗边框
    pointBackgroundColor: color,
    pointBorderColor: '#111111',
    pointHoverRadius: 8,
    pointHoverBorderWidth: 4,
  };
}

/**
 * 获取 Neobrutalism 风格的柱状图数据集配置
 */
export function getNeobrutBarDataset(options: {
  label: string;
  data: number[];
  color: string;
  yAxisID?: string;
  stack?: string;
}) {
  const { label, data, color, yAxisID = 'y', stack = '' } = options;

  return {
    type: 'bar' as const,
    label,
    data,
    backgroundColor: color,
    borderColor: '#111111', // 统一黑色边框
    borderWidth: 3, // 粗边框
    borderRadius: 0, // 方形柱状图
    yAxisID,
    order: 1,
    stack,
  };
}

/**
 * 获取 Neobrutalism 风格的饼图/环形图数据集配置
 */
export function getNeobrutDoughnutDataset(options: {
  data: number[];
  colors: string[];
}) {
  const { data, colors } = options;

  return {
    data,
    backgroundColor: colors,
    borderColor: '#111111', // 统一黑色边框
    borderWidth: 4, // 粗边框
    hoverBorderWidth: 5, // 悬停时更粗
    borderRadius: 0, // 无圆角
  };
}
