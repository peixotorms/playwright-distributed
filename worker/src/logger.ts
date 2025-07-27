import type { WorkerConfig, LogLevel } from "./config.js";

export class Logger {
    private workerId: string;
    private logLevel: number;
    private format: 'json' | 'text';
    private levels: { [K in LogLevel]: number } = { 'debug': 0, 'info': 1, 'warn': 2, 'error': 3 };
    private logFn: (level: LogLevel, message: string, data?: Record<string, any>) => void;

    constructor(workerId: string, level: WorkerConfig['logging']['level'] = 'info', format: WorkerConfig['logging']['format'] = 'json') {
        this.workerId = workerId;
        this.logLevel = this.levels[level] as number;
        this.format = format;

        if (this.format === 'text') {
            this.logFn = this.logText;
        } else {
            this.logFn = this.logJson;
        }
    }

    private logJson(level: LogLevel, message: string, data?: Record<string, any>): void {
        const entry = {
            timestamp: new Date().toISOString(),
            level,
            message,
            workerId: this.workerId,
            ...(data && { data })
        };
        console.log(JSON.stringify(entry));
    }

    private logText(level: LogLevel, message: string, data?: Record<string, any>): void {
        const timestamp = new Date().toISOString();
        const dataString = data ? `\n${JSON.stringify(data, null, 2)}` : '';
        console.log(`${timestamp} [${level.toUpperCase()}] [${this.workerId}] ${message}${dataString}`);
    }

    private log(level: LogLevel, message: string, data?: Record<string, any>): void {
        if ((this.levels[level] as number) < this.logLevel) {
            return;
        }
        this.logFn(level, message, data);
    }

    debug(message: string, data?: Record<string, any>): void {
        this.log('debug', message, data);
    }

    info(message: string, data?: Record<string, any>): void {
        this.log('info', message, data);
    }

    warn(message: string, data?: Record<string, any>): void {
        this.log('warn', message, data);
    }

    error(message: string, data?: Record<string, any>): void {
        this.log('error', message, data);
    }
}