// generated by stringer -type=MYSQL_COMMAND; DO NOT EDIT

package mysql

import "fmt"

const _MYSQL_COMMAND_name = "COM_SLEEPCOM_QUITCOM_INIT_DBCOM_QUERYCOM_FIELD_LISTCOM_CREATE_DBCOM_DROP_DBCOM_REFRESHCOM_SHUTDOWNCOM_STATISTICSCOM_PROCESS_INFOCOM_CONNECTCOM_PROCESS_KILLCOM_DEBUGCOM_PINGCOM_TIMECOM_DELAYED_INSERTCOM_CHANGE_USERCOM_BINLOG_DUMPCOM_TABLE_DUMPCOM_CONNECT_OUTCOM_REGISTER_SLAVECOM_STMT_PREPARECOM_STMT_EXECUTECOM_STMT_SEND_LONG_DATACOM_STMT_CLOSECOM_STMT_RESETCOM_SET_OPTIONCOM_STMT_FETCHCOM_DAEMONCOM_BINLOG_DUMP_GTIDCOM_RESET_CONNECTION"

var _MYSQL_COMMAND_index = [...]uint16{0, 9, 17, 28, 37, 51, 64, 75, 86, 98, 112, 128, 139, 155, 164, 172, 180, 198, 213, 228, 242, 257, 275, 291, 307, 330, 344, 358, 372, 386, 396, 416, 436}

func (i MYSQL_COMMAND) String() string {
	if i+1 >= MYSQL_COMMAND(len(_MYSQL_COMMAND_index)) {
		return fmt.Sprintf("MYSQL_COMMAND(%d)", i)
	}
	return _MYSQL_COMMAND_name[_MYSQL_COMMAND_index[i]:_MYSQL_COMMAND_index[i+1]]
}
