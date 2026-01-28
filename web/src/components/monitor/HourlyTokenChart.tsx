import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { CartesianGrid, LabelList, Line, LineChart, XAxis } from 'recharts';
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

interface HourlyTokenChartProps {
  data: UsageData | null;
  loading: boolean;
  isDark: boolean;
}

type HourRange = 6 | 12 | 24;

export function HourlyTokenChart({ data, loading }: HourlyTokenChartProps) {
  const { t } = useTranslation();
  const [hourRange, setHourRange] = useState<HourRange>(12);

  // 按小时聚合 Token 数据
  const hourlyData = useMemo(() => {
    if (!data?.apis) return [];

    const now = new Date();
    const cutoffTime = new Date(now.getTime() - hourRange * 60 * 60 * 1000);
    cutoffTime.setMinutes(0, 0, 0);
    const hoursCount = hourRange + 1;

    // 生成所有小时的时间点
    const allHours: string[] = [];
    for (let i = 0; i < hoursCount; i++) {
      const hourTime = new Date(cutoffTime.getTime() + i * 60 * 60 * 1000);
      const hourKey = hourTime.toISOString().slice(0, 13); // YYYY-MM-DDTHH
      allHours.push(hourKey);
    }

    // 初始化所有小时的数据为0
    const hourlyStats: Record<string, number> = {};
    allHours.forEach((hour) => {
      hourlyStats[hour] = 0;
    });

    // 收集每小时的 Token 数据（只统计成功请求）
    Object.values(data.apis).forEach((apiData) => {
      Object.values(apiData.models).forEach((modelData) => {
        modelData.details.forEach((detail) => {
          // 跳过失败请求
          if (detail.failed) return;

          const timestamp = new Date(detail.timestamp);
          if (timestamp < cutoffTime) return;

          const hourKey = timestamp.toISOString().slice(0, 13);
          if (hourlyStats[hourKey] !== undefined) {
            hourlyStats[hourKey] += detail.tokens.total_tokens || 0;
          }
        });
      });
    });

    // 转换为图表数据
    return allHours.sort().map((hour) => {
      const date = new Date(hour + ':00:00Z');
      return {
        hour: `${date.getHours()}:00`,
        tokens: (hourlyStats[hour] || 0) / 1000, // 转换为 K
      };
    });
  }, [data, hourRange]);

  // 图表配置
  const chartConfig: ChartConfig = {
    tokens: {
      label: t('monitor.hourly_token.total') + ' (K)',
      color: 'var(--chart-1)',
    },
  };

  // 获取时间范围标签
  const hourRangeLabel = useMemo(() => {
    if (hourRange === 6) return t('monitor.hourly.last_6h');
    if (hourRange === 12) return t('monitor.hourly.last_12h');
    return t('monitor.hourly.last_24h');
  }, [hourRange, t]);

  return (
    <Card className={styles.chartCard}>
      <CardHeader className={styles.chartHeader}>
        <div>
          <CardTitle className={styles.chartTitle}>{t('monitor.hourly_token.title')}</CardTitle>
          <CardDescription className={styles.chartSubtitle}>
            {hourRangeLabel}
          </CardDescription>
        </div>
        <div className={styles.chartControls}>
          <button
            className={`${styles.chartControlBtn} ${hourRange === 6 ? styles.active : ''}`}
            onClick={() => setHourRange(6)}
          >
            {t('monitor.hourly.last_6h')}
          </button>
          <button
            className={`${styles.chartControlBtn} ${hourRange === 12 ? styles.active : ''}`}
            onClick={() => setHourRange(12)}
          >
            {t('monitor.hourly.last_12h')}
          </button>
          <button
            className={`${styles.chartControlBtn} ${hourRange === 24 ? styles.active : ''}`}
            onClick={() => setHourRange(24)}
          >
            {t('monitor.hourly.last_24h')}
          </button>
        </div>
      </CardHeader>

      <CardContent className={styles.chartContent}>
        {loading || hourlyData.length === 0 ? (
          <div className={styles.chartEmpty}>
            {loading ? t('common.loading') : t('monitor.no_data')}
          </div>
        ) : (
          <ChartContainer
            config={chartConfig}
            className={`${styles.lineChartContainer} [&_.recharts-layer_path]:stroke-black [&_.recharts-layer_path]:dark:stroke-white`}
          >
            <LineChart
              accessibilityLayer
              data={hourlyData}
              margin={{
                top: 20,
                left: 12,
                right: 12,
              }}
            >
              <CartesianGrid vertical={false} />
              <XAxis
                dataKey="hour"
                tickLine={false}
                axisLine={false}
                tickMargin={8}
              />
              <ChartTooltip
                cursor={false}
                content={<ChartTooltipContent indicator="line" />}
              />
              <Line
                dataKey="tokens"
                type="monotone"
                stroke="var(--color-tokens)"
                strokeWidth={2}
                dot={{
                  fill: "var(--color-tokens)",
                }}
                activeDot={{
                  r: 6,
                }}
              >
                <LabelList
                  position="top"
                  offset={12}
                  className="fill-foreground"
                  fontSize={12}
                  formatter={(value) => typeof value === 'number' ? value.toFixed(1) + 'K' : value}
                />
              </Line>
            </LineChart>
          </ChartContainer>
        )}
      </CardContent>
    </Card>
  );
}
