import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Cell, Label, Pie, PieChart } from 'recharts';
import type { UsageData } from '@/pages/MonitorPage';
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/Card';
import {
  ChartConfig,
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
} from '@/components/ui/chart';
import styles from '@/pages/MonitorPage.module.scss';

interface ModelDistributionChartProps {
  data: UsageData | null;
  loading: boolean;
  isDark: boolean;
  timeRange: number;
}

type ViewMode = 'request' | 'token';

// 预定义颜色列表
const CHART_COLORS = [
  'var(--chart-1)',
  'var(--chart-2)',
  'var(--chart-3)',
  'var(--chart-4)',
  'var(--chart-5)',
  '#06b6d4',
  '#eab308',
  '#ef4444',
  '#14b8a6',
  '#6366f1',
];

export function ModelDistributionChart({ data, loading, timeRange }: ModelDistributionChartProps) {
  const { t } = useTranslation();
  const [viewMode, setViewMode] = useState<ViewMode>('request');

  const timeRangeLabel = timeRange === 1
    ? t('monitor.today')
    : t('monitor.last_n_days', { n: timeRange });

  // 计算模型分布数据
  const distributionData = useMemo(() => {
    if (!data?.apis) return [];

    const modelStats: Record<string, { requests: number; tokens: number }> = {};

    Object.values(data.apis).forEach((apiData) => {
      Object.entries(apiData.models).forEach(([modelName, modelData]) => {
        if (!modelStats[modelName]) {
          modelStats[modelName] = { requests: 0, tokens: 0 };
        }
        modelData.details.forEach((detail) => {
          modelStats[modelName].requests++;
          modelStats[modelName].tokens += detail.tokens.total_tokens || 0;
        });
      });
    });

    // 转换为数组并排序
    const sorted = Object.entries(modelStats)
      .map(([name, stats]) => ({
        name,
        requests: stats.requests,
        tokens: stats.tokens,
      }))
      .sort((a, b) => {
        if (viewMode === 'request') {
          return b.requests - a.requests;
        }
        return b.tokens - a.tokens;
      });

    // 取 Top 10
    return sorted.slice(0, 10);
  }, [data, viewMode]);

  // 计算总数
  const total = useMemo(() => {
    return distributionData.reduce((sum, item) => {
      return sum + (viewMode === 'request' ? item.requests : item.tokens);
    }, 0);
  }, [distributionData, viewMode]);

  // 生成安全的 key (用于 CSS 变量)
  const getSafeKey = (_name: string, index: number) => `model${index}`;

  // 准备图表数据 - 使用 --color-* 变量
  const chartData = useMemo(() => {
    return distributionData.map((item) => ({
      name: item.name,
      value: viewMode === 'request' ? item.requests : item.tokens,
    }));
  }, [distributionData, viewMode]);

  // 图表配置 - 定义颜色
  const chartConfig = useMemo((): ChartConfig => {
    const config: ChartConfig = {
      value: {
        label: viewMode === 'request' ? t('monitor.requests') : 'Tokens',
      },
    };

    distributionData.forEach((item, index) => {
      config[getSafeKey(item.name, index)] = {
        label: item.name,
        color: CHART_COLORS[index % CHART_COLORS.length],
      };
    });

    return config;
  }, [distributionData, viewMode, t]);

  // 格式化数值
  const formatValue = (value: number) => {
    if (value >= 1000000) {
      return (value / 1000000).toFixed(1) + 'M';
    }
    if (value >= 1000) {
      return (value / 1000).toFixed(1) + 'K';
    }
    return value.toString();
  };

  return (
    <Card className={styles.chartCard}>
      <CardHeader className={styles.chartHeader}>
        <div>
          <CardTitle className={styles.chartTitle}>{t('monitor.distribution.title')}</CardTitle>
          <CardDescription className={styles.chartSubtitle}>
            {timeRangeLabel} · {viewMode === 'request' ? t('monitor.distribution.by_requests') : t('monitor.distribution.by_tokens')}
            {' · Top 10'}
          </CardDescription>
        </div>
        <div className={styles.chartControls}>
          <button
            className={`${styles.chartControlBtn} ${viewMode === 'request' ? styles.active : ''}`}
            onClick={() => setViewMode('request')}
          >
            {t('monitor.distribution.requests')}
          </button>
          <button
            className={`${styles.chartControlBtn} ${viewMode === 'token' ? styles.active : ''}`}
            onClick={() => setViewMode('token')}
          >
            {t('monitor.distribution.tokens')}
          </button>
        </div>
      </CardHeader>

      <CardContent className={styles.chartContent}>
        {loading || distributionData.length === 0 ? (
          <div className={styles.chartEmpty}>
            {loading ? t('common.loading') : t('monitor.no_data')}
          </div>
        ) : (
          <div className={styles.distributionContent}>
            <div className={styles.donutWrapper}>
              <ChartContainer
                config={chartConfig}
                className="mx-auto aspect-square max-h-[250px]"
              >
                <PieChart>
                  <ChartTooltip
                    cursor={false}
                    content={<ChartTooltipContent hideLabel />}
                  />
                <Pie
                  data={chartData}
                  dataKey="value"
                  nameKey="name"
                  innerRadius={60}
                  strokeWidth={2}
                >
                  {chartData.map((entry, index) => (
                    <Cell
                      key={`${entry.name}-${index}`}
                      fill={`var(--color-${getSafeKey(entry.name, index)})`}
                    />
                  ))}
                  <Label
                    content={({ viewBox }) => {
                      if (viewBox && "cx" in viewBox && "cy" in viewBox) {
                          return (
                            <text
                              x={viewBox.cx}
                              y={viewBox.cy}
                              textAnchor="middle"
                              dominantBaseline="middle"
                            >
                              <tspan
                                x={viewBox.cx}
                                y={viewBox.cy}
                                className="fill-foreground text-3xl font-bold"
                              >
                                {total.toLocaleString()}
                              </tspan>
                              <tspan
                                x={viewBox.cx}
                                y={(viewBox.cy || 0) + 24}
                                className="fill-foreground"
                              >
                                {viewMode === 'request' ? t('monitor.distribution.request_share') : t('monitor.distribution.token_share')}
                              </tspan>
                            </text>
                          );
                        }
                      }}
                    />
                  </Pie>
                </PieChart>
              </ChartContainer>
            </div>
            <div className={styles.legendList}>
              {distributionData.map((item, index) => {
                const value = viewMode === 'request' ? item.requests : item.tokens;
                const percentage = total > 0 ? ((value / total) * 100).toFixed(1) : '0';
                return (
                  <div key={item.name} className={styles.legendItem}>
                  <span
                    className={styles.legendDot}
                    style={{ backgroundColor: `var(--color-${getSafeKey(item.name, index)})` }}
                  />
                    <span className={styles.legendName} title={item.name}>
                      {item.name}
                    </span>
                    <span className={styles.legendValue}>
                      {formatValue(value)} ({percentage}%)
                    </span>
                  </div>
                );
              })}
            </div>
          </div>
        )}
      </CardContent>
    </Card>
  );
}
